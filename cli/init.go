package cli

import (
	"io/ioutil"
	"os"
	"os/exec"
	"os/user"
	"strconv"

	log "github.com/sirupsen/logrus"
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

func initialize() {
	var r = prepare(needs{db: true})

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

	if _, err := user.Lookup("coordinator"); err != nil {
		if _, ok := err.(user.UnknownUserError); ok {
			if output, err := exec.Command("useradd", "--user-group", "--no-create-home", "--shell=/bin/false", "-Gdocker", "coordinator").CombinedOutput(); err != nil {
				log.WithError(err).Errorf("Unable to create coordinator user.\n%s", output)
			}
		} else {
			log.WithError(err).Error("Unable to locate existing system user.")
		}
	}

	group, err := user.LookupGroup("coordinator")
	if err != nil || group == nil {
		log.WithError(err).Fatal("Unable to locate created coordinator group.")
	}
	gid64, err := strconv.ParseInt(group.Gid, 10, 32)
	if err != nil {
		log.WithError(err).Fatal("Unable to parse coordinator group ID.")
	}
	gid := int(gid64)

	if err := os.MkdirAll("/etc/az-coordinator", 0770); err != nil {
		log.WithError(err).Error("Unable to create coordinator configuration directory.")
	}
	if err := os.Chown("/etc/az-coordinator", -1, gid); err != nil {
		log.WithError(err).Error("Unable to change coordinator configuration directory ownership.")
	}

	if err := os.MkdirAll("/etc/ssl/az/", 0770); err != nil {
		log.WithError(err).Error("Unable to create TLS credential directory.")
	}
	if err := os.Chown("/etc/ssl/az", -1, gid); err != nil {
		log.WithError(err).Error("Unable to change TLS credential directory ownership.")
	}

	if err := ioutil.WriteFile("/etc/dbus-1/system.d/az-coordinator.conf", []byte(dbusConf), 0644); err != nil {
		log.WithError(err).Error("Unable to write DBus configuration file.")
	}

	if err := ioutil.WriteFile("/etc/polkit-1/rules.d/00-coordinator.conf", []byte(polkitConf), 0644); err != nil {
		log.WithError(err).Error("Unable to write polkit configuration file.")
	}

	if err := os.Chown("/etc/systemd/system", -1, gid); err != nil {
		log.WithError(err).Error("Unable to change systemd unit file directory ownership.")
	}

	log.Info("Initialization complete.")
}
