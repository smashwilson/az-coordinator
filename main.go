package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"os/exec"
	"os/user"
	"strconv"

	_ "github.com/lib/pq"
	log "github.com/sirupsen/logrus"
	"github.com/smashwilson/az-coordinator/secrets"
	"github.com/smashwilson/az-coordinator/state"
)

func main() {
	var (
		verbose = false
		quiet   = false
		help    = false
	)

	const verboseDescription = "Log everything that may be logged."
	flag.BoolVar(&verbose, "verbose", false, verboseDescription)
	flag.BoolVar(&verbose, "v", false, verboseDescription)

	const quietDescription = "Log only warnings and errors."
	flag.BoolVar(&quiet, "quiet", false, quietDescription)
	flag.BoolVar(&quiet, "q", false, quietDescription)

	const helpDescription = "Show this message."
	flag.BoolVar(&help, "help", false, helpDescription)
	flag.BoolVar(&help, "h", false, helpDescription)

	flag.Parse()

	if verbose && quiet {
		log.Error("-verbose and -quiet may not be provided together.")
		writeHelp(os.Stderr, 1)
	}

	if verbose {
		log.SetLevel(log.TraceLevel)
	}
	if quiet {
		log.SetLevel(log.WarnLevel)
	}

	if help {
		writeHelp(os.Stdout, 0)
	}

	if flag.NArg() < 1 {
		log.Error("You must provide at least one command.")
		writeHelp(os.Stderr, 1)
	}

	if fn, ok := commands[flag.Arg(0)]; ok {
		fn()
	} else {
		writeHelp(os.Stderr, 1)
	}
}

var commands = map[string]func(){
	"help":        help,
	"init":        initialize,
	"set-secrets": setSecrets,
	"diff":        diff,
	"sync":        sync,
	"serve":       serve,
}

func help() {
	writeHelp(os.Stdout, 0)
}

func writeHelp(out io.Writer, exitCode int) {
	fmt.Fprintf(out, "Usage: %s [flags] [command]\n", os.Args[0])
	fmt.Fprintf(out, "\n")
	fmt.Fprintf(out, "Flags:\n")
	fmt.Fprintf(out, "\n")
	fmt.Fprintf(out, "  --verbose,-v  Log everything that can be logged.\n")
	fmt.Fprintf(out, "  --quiet,-q    Log only errors and warnings.\n")
	fmt.Fprintf(out, "\n")
	fmt.Fprintf(out, "Commands:\n")
	fmt.Fprintf(out, "\n")
	fmt.Fprintf(out, "  help         Show this message.\n")
	fmt.Fprintf(out, "  init         Bootstrap the host and database if needed. Run as root.\n")
	fmt.Fprintf(out, "  set-secrets  Add or override existing secrets from a JSON file.\n")
	fmt.Fprintf(out, "  diff         Calculate the actions needed to be taken to bring the system to its desired state.\n")
	fmt.Fprintf(out, "  sync         Bring the system to its desired state. Report the actions taken.\n")
	fmt.Fprintf(out, "  serve        Begin the server that hosts the management API.\n")
	os.Exit(exitCode)
}

const dbusConf = `<!DOCTYPE busconfig PUBLIC "-//freedesktop//DTD D-Bus Bus Configuration 1.0//EN"
"http://www.freedesktop.org/standards/dbus/1.0/busconfig.dtd">
<busconfig>
	<policy user="coordinator">
		<allow send_destination="org.freedesktop.systemd1" send_interface="org.freedesktop.systemd1.Manager" send_member="GetUnit" />
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

func setSecrets() {
	if flag.NArg() < 2 {
		fmt.Fprintf(os.Stderr, "set-secrets requires at least one argument: the path to a JSON file.\n")
		writeHelp(os.Stderr, 1)
	}

	var r = prepare(needs{options: true, db: true})

	var toLoad map[string]string
	inf, err := os.Open(flag.Arg(1))
	if err != nil {
		log.WithError(err).WithField("path", flag.Arg(1)).Fatal("Unable to load secrets file.")
	}
	decoder := json.NewDecoder(inf)
	if err = decoder.Decode(&toLoad); err != nil {
		log.WithError(err).WithField("path", flag.Arg(1)).Fatal("Unable to parse secrets file.")
	}

	log.Info("Creating decoder ring.")
	ring, err := secrets.NewDecoderRing(r.options.MasterKeyID, r.options.AWSRegion)
	if err != nil {
		log.WithError(err).Fatal("Unable to create decoder ring.")
	}

	log.Info("Loading and decrypting existing secrets.")
	bag, err := secrets.LoadFromDatabase(r.db, ring)
	if err != nil {
		log.WithError(err).Fatal("Unable to load and decrypt existing secrets.")
	}
	log.WithField("count", bag.Len()).Info("Secrets loaded successfully.")

	for k, v := range toLoad {
		bag.Set(k, v)
	}

	if err = bag.SaveToDatabase(r.db, ring); err != nil {
		log.WithError(err).Fatal("Unable to encrypt and save new secrets.")
	}

	log.WithFields(log.Fields{"count": bag.Len(), "added": len(toLoad)}).Info("Secrets added successfully.")
}

func diff() {
	var r = prepare(needs{session: true})

	log.Info("Reading desired state.")
	desired, err := r.session.ReadDesiredState()
	if err != nil {
		log.WithError(err).Fatal("Unable to read desired state.")
	}

	if err = desired.ReadImages(r.session); err != nil {
		log.WithError(err).Fatal("Unable to read Docker images.")
	}

	log.Info("Reading actual state.")
	actual, err := r.session.ReadActualState()
	if err != nil {
		log.WithError(err).Fatal("Unable to read actual state.")
	}

	log.Info("Computing delta.")
	delta := r.session.Between(desired, actual)

	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(delta); err != nil {
		log.Fatalf("Unable to write JSON: %v.\n", err)
	}
}

func sync() {
	r := prepare(needs{session: true})
	delta := performSync(r.session)

	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(delta); err != nil {
		log.WithError(err).Fatal("Unable to write JSON.")
	}
}

func serve() {
	r := prepare(needs{
		options: true,
		ring:    true,
		session: true,
		db:      true,
	})

	log.Info("Performing initial sync.")
	delta := performSync(r.session)
	log.WithField("delta", delta).Debug("Delta applied.")

	s := newServer(r.options, r.db, r.ring)
	if err := s.listen(); err != nil {
		log.Fatal(err)
	}
}

func performSync(session *state.Session) state.Delta {
	log.Info("Reading desired state.")
	desired, err := session.ReadDesiredState()
	if err != nil {
		log.WithError(err).Fatal("Unable to read desired state.")
	}

	log.Info("Pulling referenced images.")
	if errs := session.PullAllImages(*desired); len(errs) > 0 {
		for _, err := range errs {
			log.WithError(err).Warn("Pull error")
		}
		log.Warn("Encountered errors when pulling images.")
	}

	log.Info("Reading updated docker images.")
	if err = desired.ReadImages(session); err != nil {
		log.WithError(err).Fatal("Unable to read docker image IDs.")
	}

	log.Info("Reading actual state.")
	actual, err := session.ReadActualState()
	if err != nil {
		log.WithError(err).Fatal("Unable to read actual state.")
	}

	log.Info("Computing delta.")
	delta := session.Between(desired, actual)

	if errs := delta.Apply(); len(errs) > 0 {
		for _, err := range errs {
			log.WithError(err).Warn("Delta application error.")
		}
		log.Warn("Unable to apply delta.")
	}

	return delta
}
