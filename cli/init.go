package cli

import (
	"fmt"
	"io/ioutil"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"

	log "github.com/sirupsen/logrus"
	"github.com/smashwilson/az-coordinator/config"
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

func ensureGroup(groupName string) {
	if _, err := user.LookupGroup(groupName); err != nil {
		if _, ok := err.(user.UnknownGroupError); ok {
			if output, err := exec.Command("groupadd", groupName).CombinedOutput(); err != nil {
				log.WithFields(log.Fields{
					"err":       err,
					"groupName": groupName,
				}).Fatalf("Unable to create group.\n%s", output)
			}
		}

		log.WithFields(log.Fields{
			"err":       err,
			"groupName": groupName,
		}).Fatalf("Unable to search for existing group.")
	}

	log.WithField("groupName", groupName).Debug("Group already exists.")
}

func ensureUser(userName string, groupNames ...string) {
	existing, err := user.Lookup(userName)
	if err != nil {
		if _, ok := err.(user.UnknownUserError); ok {
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
		} else {
			log.WithFields(log.Fields{
				"err":      err,
				"userName": userName,
			}).Fatalf("Unable to search for existing user.")
		}
	} else {
		log.WithField("userName", userName).Debug("User already exists.")
	}

	expectedGids := make(map[string]bool, len(groupNames))
	for _, groupName := range groupNames {
		g, err := user.LookupGroup(groupName)
		if err != nil {
			log.WithFields(log.Fields{
				"err":       err,
				"groupName": groupName,
			}).Fatal("Unable to locate existing group.")
		}

		expectedGids[g.Gid] = true
	}

	actualGids, err := existing.GroupIds()
	if err != nil {
		log.WithFields(log.Fields{
			"err":      err,
			"userName": userName,
		}).Fatal("Unable to enumerate groups that user belongs to.")
	}

	hasMissing := false
	for _, actualGid := range actualGids {
		if _, ok := expectedGids[actualGid]; ok {
			delete(expectedGids, actualGid)
		} else {
			hasMissing = true
		}
	}
	hasExtra := len(expectedGids) > 0

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

	ensureGroup("azinfra")
	ensureUser("coordinator", "az-infra")

	gid := getGid("azinfra")

	ensureDirectory(filepath.Dir(config.DefaultOptionsPath), gid)
	ensureDirectory("/etc/ssl/az", gid)
	ensureDirectory("/etc/systemd/system", gid)

	if err := ioutil.WriteFile("/etc/dbus-1/system.d/az-coordinator.conf", []byte(dbusConf), 0644); err != nil {
		log.WithError(err).Error("Unable to write DBus configuration file.")
	}

	if err := ioutil.WriteFile("/etc/polkit-1/rules.d/00-coordinator.conf", []byte(polkitConf), 0644); err != nil {
		log.WithError(err).Error("Unable to write polkit configuration file.")
	}

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

	if err := os.Chown(config.DefaultOptionsPath, -1, gid); err != nil {
		log.WithFields(log.Fields{
			"err":                err,
			"defaultOptionsPath": config.DefaultOptionsPath,
			"gid":                gid,
		}).Fatal("Unable to modify options file ownership.")
	}

	if err := os.Chmod(config.DefaultOptionsPath, 0640); err != nil {
		log.WithFields(log.Fields{
			"err":                err,
			"defaultOptionsPath": config.DefaultOptionsPath,
		}).Fatal("Unable to modify options file permissions.")
	}

	log.Info("Initialization complete.")
}
