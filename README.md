# Azurefire Coordinator

## Or: "Oops, I Wrote A Docker Container Orchestration Engine"

![status badge](https://github.com/smashwilson/az-coordinator/workflows/ci/badge.svg)

This is a solution to a very specific problem that I have. Namely:

* I keep an AWS server running to host side-projects that need a process to run somewhere. Mostly this is [@pushbot](https://github.com/smashwilson/pushbot).
* Time and energy to devote attention to the server is sporadic and unpredictable. So, I'm running [Container Linux](https://coreos.com/os/docs/latest/), which auto-patches continuously, so I don't have to deal with issues like "oh no I haven't run `apt-get update` in literally three years".
* This means that I need to package the stuff I want to run into Docker containers (easy) and manage it with systemd units (not too bad).
* But, I'm allergic to running systems that I can't reproduce from scratch in fifteen minutes or less while I'd rather be doing something else. That's a little complicated here, because Container Linux, by design, ships with virtually _nothing_ outside of Docker. I _could_ drop a Python interpreter somewhere and use Ansible to do my bootstrapping. But that always felt messy to me.
* I'd also like to be able to branch-deploy a container image built from a pull request against one of the services running here. I technically can do this now, but it takes about four minutes to launch, which is slow enough that I almost never bother.

So, this repository builds a single Go binary that can be dropped on a freshly provisioned Container Linux server to bootstrap it from nothing to running all of the services I care about, where the definition of "services I care about" is allowed to easily vary. It then binds to a high port (8443) and serves an admin API that can be used to add, change, or remove services on the fly.

### What it does

* Configures itself from a single JSON file.
* Reads desired system state from a PostgreSQL database I keep elsewhere.
* Introspects the local Docker daemon and systemd unit files to determine current system state.
* Writes systemd unit files to disk based on a limited set of fixed templates. Mostly, these run Docker containers with configurable environment variables.
* Manages secrets stored in the database (encrypted via [KMS](https://aws.amazon.com/kms/) keys) and made available to containers as additional environment variables.
* Creates, deletes, or reloads systemd unit files as necessary.
* Supports [CORS](https://developer.mozilla.org/en-US/docs/Web/HTTP/CORS) headers so I can use this from [pushbot.party](https://pushbot.party/) without needing yet another backend.

### What it doesn't do

* Work with any containers that aren't in my [DockerHub](https://hub.docker.com/) account or unit files that aren't named `az-...`.
* Manage inter-service dependencies.
* Provide zero-downtime rollovers or horizontal scaling.

### Getting it to run the first time

A reference for myself after I leave this alone for six months and inevitably forget how it all works. Ideally, this will all be done from automated builds in [smashwilson/az-infra](https://github.com/smashwilson/az-infra). Just in case, though, here's how to launch it completely from scratch:

(1) Create a PostgreSQL database. Create a new user account and grant it access. Pre-create the "secrets" table for good measure.

```sql
CREATE TABLE secrets (key TEXT NOT NULL, ciphertext bytea NOT NULL)
```

(2) Create a secrets file with the TLS certificate, private key, and DH parameters:

```json
{
  "TLS_CERTIFICATE": "-----BEGIN CERTIFICATE-----\n....",
  "TLS_KEY": "-----BEGIN NOT A REAL PRIVATE KEY-----\n....",
  "TLS_DH_PARAMS": "-----BEGIN DH PARAMETERS-----\n...."
}
```

(3) Create an options file:

```json
{
  "listen_address": "0.0.0.0:8443",
  "database_url": "<connection URL>",
  "auth_token": "<random gibberish>",
  "master_key_id": "<from KMS dashboard>",
  "aws_region": "us-east-1",
  "docker_api_version": "1.38",
  "allowed_origin": "https://pushhbot.party"
}
```

(4) Build the binary:

```sh
$ go build
```

(5) Bootstrap the secrets table.

```sh
# AWS credentials that grant at least KMS:Decrypt and KMS:GenerateDataKey.
$ source /keybase/private/smashwilson/aws/me.sh

$ AZ_OPTIONS=/path/to/options.json ./az-coordinator set-secrets /path/to/secrets.json
```

(6) Cross-compile a Linux binary. Drop it and the options file on the AWS host.

```sh
$ export GOOS=linux
$ export GOARCH=amd64
$ go build
$ scp ./az-coordinator core@${AZ_HOST}:az-coordinator
$ scp /path/to/options.json core@${AZ_HOST}:options.json
```

(7) Run the `init` command on the host with `sudo`.

```sh
$ ssh core@${AZ_HOST}

# On AZ_HOST:
# It's nice to have it on the ${PATH}
$ sudo mkdir -p /opt/bin
$ sudo mv ./az-coordinator /opt/bin/coordinator

# Bootstrap all the things
$ sudo AZ_OPTIONS=options.json az-coordinator -v init
```

The `init` command performs an initial sync, so everything should be running now. :tada:
