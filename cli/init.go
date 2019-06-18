package cli

import (
	"fmt"
	"io/ioutil"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	log "github.com/sirupsen/logrus"
	"github.com/smashwilson/az-coordinator/config"
	"github.com/smashwilson/az-coordinator/secrets"
	"github.com/smashwilson/az-coordinator/state"
)

const dbusConf = `<!DOCTYPE busconfig PUBLIC "-//freedesktop//DTD D-Bus Bus Configuration 1.0//EN"
"http://www.freedesktop.org/standards/dbus/1.0/busconfig.dtd">
<busconfig>
	<policy user="coordinator">
		<allow send_destination="org.freedesktop.systemd1" send_interface="org.freedesktop.systemd1.Manager" send_member="GetUnit" />
		<allow send_destination="org.freedesktop.systemd1" send_interface="org.freedesktop.systemd1.Manager" send_member="ListUnitFilesByPatterns" />
		<allow send_destination="org.freedesktop.systemd1" send_interface="org.freedesktop.systemd1.Manager" send_member="StartUnit" />
		<allow send_destination="org.freedesktop.systemd1" send_interface="org.freedesktop.systemd1.Manager" send_member="StartUnitReplace" />
		<allow send_destination="org.freedesktop.systemd1" send_interface="org.freedesktop.systemd1.Manager" send_member="StopUnit" />
		<allow send_destination="org.freedesktop.systemd1" send_interface="org.freedesktop.systemd1.Manager" send_member="ReloadUnit" />
		<allow send_destination="org.freedesktop.systemd1" send_interface="org.freedesktop.systemd1.Manager" send_member="RestartUnit" />
		<allow send_destination="org.freedesktop.systemd1" send_interface="org.freedesktop.systemd1.Manager" send_member="ReloadOrRestartUnit" />
		<allow send_destination="org.freedesktop.systemd1" send_interface="org.freedesktop.systemd1.Manager" send_member="KillUnit" />
		<allow send_destination="org.freedesktop.systemd1" send_interface="org.freedesktop.systemd1.Manager" send_member="Reload" />
		<allow send_destination="org.freedesktop.systemd1" send_interface="org.freedesktop.systemd1.Manager" send_member="EnableUnitFiles" />
		<allow send_destination="org.freedesktop.systemd1" send_interface="org.freedesktop.systemd1.Manager" send_member="DisableUnitFiles" />
	</policy>
</busconfig>`

const polkitConf = `polkit.addRule(function(action, subject) {
    if (
        subject.user == "coordinator" &&
        (action.id == "org.freedesktop.systemd1.manage-units" ||
        action.id == "org.freedesktop.systemd1.manage-unit-files" ||
        action.id == "org.freedesktop.systemd1.reload-daemon")
    ) {
        return polkit.Result.YES;
    }
})`

var groupEntryRx = regexp.MustCompile(`\A[^:]+:[^:]+:(\d+)`)

func getGroupID(groupName string) (bool, int) {
	output, err := exec.Command("getent", "group", groupName).Output()
	if err != nil {
		log.WithFields(log.Fields{
			"err":       err,
			"groupName": groupName,
		}).Fatalf("Unable to query for existing group:\n%s", output)
	}

	if len(output) == 0 {
		return false, 0
	}

	m := groupEntryRx.FindSubmatch(output)
	if len(m) != 2 {
		log.WithFields(log.Fields{
			"err":       err,
			"groupName": groupName,
		}).Fatalf("Unable to interpret getent output:\n%s", output)
	}
	gid64, err := strconv.ParseInt(string(m[1]), 10, 32)
	if err != nil {
		log.WithFields(log.Fields{
			"err": err,
			"gid": string(m[1]),
		}).Fatalf("Unable to parse gid as an integer.")
	}
	return true, int(gid64)
}

func getUserGroups(userName string) (bool, []string) {
	output, err := exec.Command("id", "-Gn", userName).Output()
	if err != nil {
		log.WithFields(log.Fields{
			"err":      err,
			"userName": userName,
		}).Fatalf("Unable to locate existing user:\n%s", output)
	}

	if len(output) == 0 {
		return false, nil
	}

	return true, strings.Split(string(output), " ")
}

func getUserID(userName string) (bool, int) {
	output, err := exec.Command("id", "-u", userName).Output()
	if err != nil {
		log.WithFields(log.Fields{
			"err":      err,
			"userName": userName,
		}).Fatalf("Unable to locate existing user:\n%s", output)
	}

	trimmed := strings.TrimSpace(string(output))

	if len(trimmed) == 0 {
		return false, 0
	}

	uid64, err := strconv.ParseInt(trimmed, 10, 32)
	if err != nil {
		log.WithFields(log.Fields{
			"err":      err,
			"userName": userName,
			"output":   trimmed,
		}).Fatalf("Unable to convert uid to integer.")
	}

	return true, int(uid64)
}

func ensureGroup(groupName string) int {
	if exists, gid := getGroupID(groupName); exists {
		log.WithFields(log.Fields{
			"groupName": groupName,
			"groupID":   gid,
		}).Debug("Group already exists.")
		return gid
	}

	if output, err := exec.Command("groupadd", groupName).CombinedOutput(); err != nil {
		log.WithFields(log.Fields{
			"err":       err,
			"groupName": groupName,
		}).Fatalf("Unable to create group.\n%s", output)
	}

	exists, gid := getGroupID(groupName)
	if !exists {
		log.WithField("groupName", groupName).Fatal("Group does not exist after creation.")
	}
	return gid
}

func ensureUser(userName string, groupNames ...string) int {
	exists, actualGroupNames := getUserGroups(userName)
	if !exists {
		args := []string{"--user-group", "--no-create-home", "--shell=/bin/false"}
		if len(groupNames) > 0 {
			args = append(args, fmt.Sprintf("-G%s", strings.Join(groupNames, ",")))
		}
		args = append(args, userName)

		if output, err := exec.Command("useradd", args...).CombinedOutput(); err != nil {
			log.WithFields(log.Fields{
				"err":      err,
				"userName": userName,
			}).Fatalf("Unable to create user.\n%s", output)
		}

		log.WithFields(log.Fields{
			"userName":   userName,
			"groupNames": groupNames,
		}).Debug("User created.")

		now, uid := getUserID(userName)
		if !now {
			log.WithField("userName", userName).Fatal("User does not exist immediately after creation.")
		}
		log.WithFields(log.Fields{
			"userName": userName,
			"userID":   uid,
		}).Debug("User ID located.")

		return uid
	}

	expectedGroupNames := make(map[string]bool, len(groupNames))
	for _, expectedGroupName := range groupNames {
		expectedGroupNames[expectedGroupName] = true
	}

	hasMissing := false
	for _, actualGroupName := range actualGroupNames {
		if _, ok := expectedGroupNames[actualGroupName]; ok {
			delete(expectedGroupNames, actualGroupName)
		} else {
			hasMissing = true
		}
	}
	hasExtra := len(expectedGroupNames) > 0

	if hasMissing || hasExtra {
		gArg := fmt.Sprintf("-G%s", strings.Join(groupNames, ","))
		if output, err := exec.Command("usermod", gArg, userName).CombinedOutput(); err != nil {
			log.WithFields(log.Fields{
				"err":        err,
				"userName":   userName,
				"groupNames": groupNames,
			}).Fatalf("Unable to modify groups of existing user.\n%s", output)
		}

		log.WithFields(log.Fields{
			"userName":   userName,
			"groupNames": groupNames,
		}).Debug("Assigned groups to existing user.")
	} else {
		log.WithFields(log.Fields{
			"userName":   userName,
			"groupNames": groupNames,
		}).Debug("Existing user has correct groups.")
	}

	now, uid := getUserID(userName)
	if !now {
		log.WithField("userName", userName).Fatal("User does not exist immediately after creation.")
	}
	log.WithFields(log.Fields{
		"userName": userName,
		"userID":   uid,
	}).Debug("User ID located.")

	return uid
}

func ensureDirectory(dirName string, gid int) {
	if err := os.MkdirAll(dirName, 0770); err != nil {
		log.WithFields(log.Fields{
			"err":     err,
			"dirName": dirName,
			"gid":     gid,
		}).Fatal("Unable to create directory.")
	}
	if err := os.Chmod(dirName, 0770); err != nil {
		log.WithFields(log.Fields{
			"err":     err,
			"dirName": dirName,
			"gid":     gid,
		}).Fatal("Unable to change directory permissions.")
	}
	if err := os.Chown(dirName, -1, gid); err != nil {
		log.WithFields(log.Fields{
			"err":     err,
			"dirName": dirName,
			"gid":     gid,
		}).Fatal("Unable to change directory ownership.")
	}

	log.WithFields(log.Fields{
		"dirName": dirName,
		"groupID": gid,
	}).Debug("Directory exists and has proper permissions and ownership.")
}

func getGid(groupName string) int {
	group, err := user.LookupGroup(groupName)
	if err != nil || group == nil {
		log.WithError(err).Fatal("Unable to locate created coordinator group.")
	}
	gid64, err := strconv.ParseInt(group.Gid, 10, 32)
	if err != nil {
		log.WithError(err).Fatal("Unable to parse coordinator group ID.")
	}
	return int(gid64)
}

func initialize() {
	var r = prepare(needs{options: true, db: true})

	if _, err := r.db.Exec(`
		CREATE TABLE IF NOT EXISTS secrets (
			key TEXT NOT NULL,
			ciphertext bytea NOT NULL
		)
	`); err != nil {
		log.WithError(err).Error("Unable to create secrets table.")
	}

	if _, err := r.db.Exec(`
		CREATE TABLE IF NOT EXISTS state_systemd_units (
			id SERIAL PRIMARY KEY,
			path TEXT NOT NULL,
			type INTEGER NOT NULL,
			container_name TEXT NOT NULL,
			container_image_name TEXT NOT NULL,
			container_image_tag TEXT NOT NULL,
			secrets JSONB NOT NULL,
			env JSONB NOT NULL,
			ports JSONB NOT NULL,
			volumes JSONB NOT NULL,
			schedule TEXT
		)
	`); err != nil {
		log.WithError(err).Error("Unable to create secrets table.")
	}

	azinfraGID := ensureGroup("azinfra")
	coordinatorUID := ensureUser("coordinator", "azinfra", "docker")

	ensureDirectory(filepath.Dir(config.DefaultOptionsPath), azinfraGID)
	ensureDirectory("/etc/ssl/az", azinfraGID)
	ensureDirectory("/etc/systemd/system", azinfraGID)

	if err := ioutil.WriteFile("/etc/dbus-1/system.d/az-coordinator.conf", []byte(dbusConf), 0644); err != nil {
		log.WithError(err).Error("Unable to write DBus configuration file.")
	}
	log.Debug("DBus permissions modified.")

	if err := ioutil.WriteFile("/etc/polkit-1/rules.d/00-coordinator.rules", []byte(polkitConf), 0644); err != nil {
		log.WithError(err).Error("Unable to write polkit configuration file.")
	}
	log.Debug("Polkit permissions modified.")

	if r.options.OptionsPath != config.DefaultOptionsPath {
		if err := os.Rename(r.options.OptionsPath, config.DefaultOptionsPath); err != nil {
			log.WithFields(log.Fields{
				"err":                err,
				"optionsPath":        r.options.OptionsPath,
				"defaultOptionsPath": config.DefaultOptionsPath,
			}).Fatal("Unable to move options file to the default path.")
		}
	} else {
		log.WithFields(log.Fields{
			"optionsPath": r.options.OptionsPath,
		}).Debug("Options path is already in the correct location.")
	}

	if err := os.Chown(config.DefaultOptionsPath, -1, azinfraGID); err != nil {
		log.WithFields(log.Fields{
			"err":                err,
			"defaultOptionsPath": config.DefaultOptionsPath,
			"gid":                azinfraGID,
		}).Fatal("Unable to modify options file ownership.")
	}

	if err := os.Chmod(config.DefaultOptionsPath, 0640); err != nil {
		log.WithFields(log.Fields{
			"err":                err,
			"defaultOptionsPath": config.DefaultOptionsPath,
		}).Fatal("Unable to modify options file permissions.")
	}

	log.WithField("keyID", r.options.MasterKeyID).Info("Creating decoder ring.")
	ring, err := secrets.NewDecoderRing(r.options.MasterKeyID, r.options.AWSRegion)
	if err != nil {
		log.WithError(err).Fatal("Unable to create decoder ring.")
	}

	log.Info("Establishing session.")
	session, err := state.NewSession(r.db, ring, r.options.DockerAPIVersion)
	if err != nil {
		log.WithError(err).Fatal("Unable to create session.")
	}

	delta, errs := session.Synchronize(state.SyncSettings{UID: coordinatorUID, GID: azinfraGID})
	if len(errs) > 0 {
		for _, err := range errs {
			log.WithError(err).Warn("Error encountered during synchronization.")
		}
		log.WithField("errorCount", len(errs)).Fatal("Unable to perform initial synchronization.")
	}
	log.Debugf("Synchronization complete.\n%s", delta)

	log.Info("Initialization complete.")
}
