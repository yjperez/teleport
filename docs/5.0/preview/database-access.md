---
title: Teleport Database Access
description: Secure and Audited Access to Postgres Databases. Documentation to outline our preview.
---

# Teleport Database Access Preview

Teleport Database Access allows organizations to use Teleport as a proxy to
provide secure access to their databases while improving both visibility and
access control.

To find out whether you can benefit from using Database Access, see if you're
facing any of the following challenges in your organization:

* Do you need to protect and segment access to your databases?
* Do you need provide SSO and auditing for the database access?
* Do you have compliance requirements for data protection and access monitoring
  like PCI/FedRAMP?

If so, Database Access might help you solve some of these challenges.

## Features

With Database Access users can:

* Provide secure access to databases without exposing them over the public
  network through Teleport's reverse tunnel subsystem.
* Control access to specific database instances as well as individual
  databases and database users through Teleport's RBAC model.
* Track individual users' access to databases as well as query activity
  through Teleport's audit log.

## Demo

<video autoPlay loop muted playsInline controls>
  <source src="https://goteleport.com/teleport/videos/database-access-preview/dbaccessdemo.mp4" type="video/mp4" />
  <source src="https://goteleport.com/teleport/videos/database-access-preview/dbaccessdemo.webm" type="video/webm" />
Your browser does not support the video tag.
</video>

## Getting Started

In this guide we will use Teleport Database Access to connect to a PostgreSQL
flavored AWS Aurora database. Here's an overview of what we will do:

1. Configure AWS Aurora database with IAM authentication.
2. Download and install Teleport and connect it to the Aurora database.
3. Connect to the Aurora database via Teleport.

### Step 1/3. Setup Aurora

In order to allow Teleport connections to an Aurora instance, it needs to support
IAM authentication.

If you don't have a database provisioned yet, create an instance of an Aurora
PostgreSQL in the [RDS control panel](https://console.aws.amazon.com/rds/home).
Make sure to choose "Standard create" database creation method and enable
"Password and IAM database authentication" in the Database Authentication dialog.

For existing Aurora instances, the status of IAM authentication is displayed on
the Configuration tab and can be enabled by modifying the database instance.

Next, create the following IAM policy attached to a user whose credentials a
Teleport process will be using to allow it to connect to the database:

```json
{
   "Version": "2012-10-17",
   "Statement": [
      {
         "Effect": "Allow",
         "Action": [
             "rds-db:connect"
         ],
         "Resource": [
             "arn:aws:rds-db:<region>:<account-id>:dbuser:<resource-id>/*"
         ]
      }
   ]
}
```

!!! note "Resource ID"
    Database resource ID is shown on the Configuration tab of a particular
    database instance in RDS control panel, under "Resource id". For regular
    RDS database it starts with `db-` prefix. For Aurora, use the database
    cluster resource ID (`cluster-`), not individual instance ID.

Finally, connect to the database and create a database account with IAM auth
support (or update an existing one). Once connected, execute the following
SQL statements to create a new database account and allow IAM auth for it:

```sql
CREATE USER alice;
GRANT rds_iam TO alice;
```

For more information about connecting to the PostgreSQL instance directly,
see Amazon [documentation](https://docs.aws.amazon.com/AmazonRDS/latest/UserGuide/USER_ConnectToPostgreSQLInstance.html).

See a more detailed description of these steps in the [reference](#aws-rdsaurora-postgresql) below.

### Step 2/3. Setup Teleport

Teleport Database Access is available starting from `6.0.0-alpha.1` pre-release.

Download the appropriate version of Teleport for your platform from the table
below, or visit our [downloads page](https://goteleport.com/teleport/download).

{% raw %}{{!docs/5.0/preview/releases-table.md!}}{% endraw %}

!!! warning
    Note, pre-releases are not suitable for production usage!

Start Teleport using the following command and point it to your Aurora database
instance. Make sure to update the database endpoint and region appropriately.

```shell
sudo teleport start \
  --roles=proxy,auth,db \
  --db-name=aurora \
  --db-protocol=postgres \
  --db-uri=postgres-aurora-instance-1.abcdefghijklm.us-west-1.rds.amazonaws.com:5432 \
  --db-aws-region=us-west-1
```

!!! note "AWS credentials"
    The node where the Teleport process is started should have AWS credentials
    configured with the policy from [step 1](#step-13-setup-aurora).

Create a Teleport user that is allowed to connect to a particular database
(e.g. `postgres`) within the Aurora instance as a particular database account
(e.g. `alice`).

```shell
sudo tctl users add alice root \
  --db-names=postgres \
  --db-users=alice
```

### Step 3/3. Connect to Database

Now that Aurora is configured with IAM authentication, Teleport is running and
the local user is created, we're ready to connect to the database.

Log into Teleport with the user we've just created. Make sure to use `tsh`
version `6.0.0-alpha.2` or newer that includes Database Access support.

For simplicity, we're using an `--insecure` flag to accept Teleport's
self-signed certificate. For production usage make sure to configure proxy
with a proper certificate/key pair. See Teleport's general
[quickstart guide](../quickstart.md#step-1c-configure-domain-name-and-obtain-tls-certificates-using-lets-encrypt).

```shell
tsh login --insecure --proxy=localhost:3080 --user=alice
```

Now we can inspect available databases and retrieve credentials for the
configured Aurora instance:

```shell
tsh db ls
tsh db login aurora
```

Finally, connect to the database using `psql` command shown in the output of
`tsh db login` command, which may look like this:

```shell
psql "service=<cluster>-aurora user=alice dbname=postgres"
```

### Next Steps

Congratulations on completing the Teleport Database Access getting started
guide!

For the next steps, dive deeper into the topics relevant to your Database
Access use-case, for example:

* Learn how to connect to a [self-hosted database](#self-hosted-postgresql).
* Learn how to configure Database Access via Teleport [configuration file](#configure-teleport).
* Learn about Database Access [RBAC](#rbac).
* See [frequently asked questions](#faq).

## Diagram

The following diagram shows an example Database Access setup:

* Root cluster provides access to an onprem instance of PostgreSQL.
* Leaf cluster, connected to the root cluster, provides access to an
  onprem instance of MySQL and PostgreSQL-compatible AWS Aurora.
* Node connects another on-premise PostgreSQL instance (perhaps, a
  metrics database) via tunnel to the root cluster.

![Teleport database access diagram](../img/dbaccess.svg)

## Release Schedule

Teleport Database Access is under active development and is available starting
from `6.0.0-alpha.1` release. The alpha version includes support for self-hosted
PostgreSQL as well as PostgreSQL compatible AWS RDS and Aurora.

See [release schedule](./upcoming-releases.md#release-schedule).

## Configure PostgreSQL

### Self-Hosted PostgreSQL

!!! note

    This section explains how to configure a self-hosted instance of PostgreSQL
    to work with Teleport Database Access. For information about configuring
    AWS RDS/Aurora see the [section below](#aws-rdsaurora-postgresql).

#### Create Certificate/Key Pair

Teleport uses mutual TLS for authentication to PostgreSQL instances. As such,
self-hosted PostgreSQL instances must be configured with Teleport's certificate
authority and a certificate/key pair that Teleport can validate.

To create these secrets, use `tctl auth sign` command. Note that it requires a
running Teleport cluster and [should be run](https://goteleport.com/teleport/docs/architecture/overview/#tctl)
on the auth server.

```sh
# Export Teleport's certificate authority and generate certificate/key pair
# for host db.example.com with a one year validity period.
$ tctl auth sign --format=db --host=db.example.com --out=server --ttl=8760h
```

Flag descriptions:

* `--format=db`: instructs the command to produce secrets in the format suitable
  for configuring a database server.
* `--host=db.example.com`: server name to encode in the certificate, should
  match the hostname Teleport will be connecting to the database at.
* `--out=server`: name prefix for output files.
* `--ttl=8760h`: certificate validity period.

The command will create 3 files: `server.cas` with Teleport's certificate
authority and `server.crt`/`server.key` with generated certificate/key pair.

!!! note "Certificate Rotation"

    Teleport signs database certificates with the host authority. As such,
    when performing [host certificates rotation](../admin-guide.md#certificate-rotation),
    the database certificates must be updated as well.

#### Configure PostgreSQL Server

To configure PostgreSQL server to accept TLS connections, add the following
to PostgreSQL configuration file `postgresql.conf`:

```conf
ssl = on
ssl_cert_file = '/path/to/server.crt'
ssl_key_file = '/path/to/server.key'
ssl_ca_file = '/path/toa/server.cas'
```

See [Secure TCP/IP Connections with SSL](https://www.postgresql.org/docs/current/ssl-tcp.html)
in PostgreSQL documentation for more details.

Additionally, PostgreSQL should be configured to require client certificate
authentication from clients connecting over TLS. This can be done by adding
the following entries to PostgreSQL host-based authentication file `pg_hba.conf`:

```conf
hostssl all             all             ::/0                    cert
hostssl all             all             0.0.0.0/0               cert
```

See [The pg_hba.conf File](https://www.postgresql.org/docs/current/auth-pg-hba-conf.html)
in PostgreSQL documentation for more details.

### AWS RDS/Aurora PostgreSQL

!!! note

    This section explains how to configure a PostgreSQL-flavored instance of
    AWS RDS or Aurora database to work with Teleport Database Access. For
    information about configuring a self-hosted PostgreSQL see the [section above](#self-hosted-postgresql).

Teleport Database Access for AWS RDS and Aurora uses IAM authentication which
can be enabled with the following steps.

#### Enable IAM Authentication

Open [Amazon RDS console](https://console.aws.amazon.com/rds/) and create a new
database instance with IAM authentication enabled, or modify an existing one to
turn it on. Make sure to use PostgreSQL database type.

See [Enabling and disabling IAM database authentication](https://docs.aws.amazon.com/AmazonRDS/latest/AuroraUserGuide/UsingWithRDS.IAMDBAuth.Enabling.html)
for more information.

#### Create IAM Policy

To allow Teleport database service to log into the database instance using auth
token, create an IAM policy and attach it to the user whose credentials the
database service will be using, for example:

```json
{
   "Version": "2012-10-17",
   "Statement": [
      {
         "Effect": "Allow",
         "Action": [
             "rds-db:connect"
         ],
         "Resource": [
             "arn:aws:rds-db:us-east-2:1234567890:dbuser:cluster-ABCDEFGHIJKL01234/*"
         ]
      }
   ]
}
```

The resource ARN in the policy has the following format:

```
arn:aws:rds-db:<region>:<account-id>:dbuser:<db-cluster-resource-id>/<db-user-name>
```

Parameters:

* `region`: AWS region where the database cluster is deployed.
* `account-id`: AWS account ID the database cluster is deployed under.
* `db-cluster-resource-id`: identifier for the database cluster, can be found
  under Configuration section in the RDS control panel.
* `db-user-name`: name of the database account to associate with IAM
  authentication. Can be a wildcard.

See [Creating and using an IAM policy for IAM database access](https://docs.aws.amazon.com/AmazonRDS/latest/AuroraUserGuide/UsingWithRDS.IAMDBAuth.IAMPolicy.html)
for more information.

#### Create Database User

Database users must have a `rds_iam` role in order to be allowed access. For
PostgreSQL:

```sql
CREATE USER alice;
GRANT rds_iam TO alice;
```

See [Creating a database account using IAM authentication](https://docs.aws.amazon.com/AmazonRDS/latest/AuroraUserGuide/UsingWithRDS.IAMDBAuth.DBAccounts.html)
for more information.

## Configure Teleport

Teleport Database Access is available starting from `6.0.0-alpha.1` pre-release.

Download the appropriate version of Teleport for your platform from the table
below, or visit our [downloads page](https://goteleport.com/teleport/download).

{% raw %}{{!docs/5.0/preview/releases-table.md!}}{% endraw %}

!!! warning
    Note, pre-releases are not suitable for production usage!

Follow the installation [instructions](../installation.md).

### Start Auth/Proxy Service

Create a configuration file for a Teleport service that will be running
auth and proxy servers:

```yaml
teleport:
  data_dir: /var/lib/teleport
  nodename: test
auth_service:
  enabled: "yes"
  cluster_name: "test"
  listen_addr: 0.0.0.0:3025
  tokens:
  - proxy,node,database:cbdeeab9-6f88-436d-a673-44d14bd86bb7
proxy_service:
  enabled: "yes"
  listen_addr: 0.0.0.0:3023
  web_listen_addr: 0.0.0.0:3080
  tunnel_listen_addr: 0.0.0.0:3024
  public_addr: teleport.example.com:3080
ssh_service:
  enabled: "no"
```

Start the service:

```sh
$ teleport start --debug --config=/path/to/teleport.yaml
```

### Start Database Service with CLI Flags

For a quick try-out, Teleport database service doesn't require a configuration
file and can be launched using a single CLI command:

```sh
$ teleport start --debug \
   --roles=db \
   --token=cbdeeab9-6f88-436d-a673-44d14bd86bb7 \
   --auth-server=teleport.example.com:3080 \
   --db-name=test \
   --db-protocol=postgres \
   --db-uri=db.example.com:5432 \
   --labels=env=test
```

Note that the `--auth-server` flag must point to cluster's proxy endpoint
because database service always connects back to the cluster over a reverse
tunnel.

Instead of using a static auth token, a short-lived dynamic token can also
be generated for a database service:

```sh
$ tctl tokens add \
    --type=db \
    --db-name=test \
    --db-protocol=postgres \
    --db-uri=db.example.com:5432
```

### Start Database Service with Config File

Below is an example of a database service configuration file that proxies
a single AWS Aurora database:

```yaml
teleport:
  data_dir: /var/lib/teleport-db
  nodename: test
  # Auth token to connect to the auth server.
  auth_token: cbdeeab9-6f88-436d-a673-44d14bd86bb7
  # Proxy address to connect to. Note that it has to be the proxy address
  # because database service always connects to the cluster over reverse
  # tunnel.
  auth_servers:
  - teleport.example.com:3080
db_service:
  enabled: "yes"
  # This section contains definitions of all databases proxied by this
  # service, can contain multiple items.
  databases:
    # Name of the database proxy instance, used to reference in CLI.
  - name: "aurora"
    # Free-form description of the database proxy instance.
    description: "AWS Aurora instance of PostgreSQL 13.0"
    # Database protocol.
    protocol: "postgres"
    # Database address, example of a AWS Aurora endpoint in this case.
    uri: "postgres-aurora-instance-1.xxx.us-east-1.rds.amazonaws.com:5432"
    # AWS specific configuration, only required for RDS and Aurora.
    aws:
      # Region the database is deployed in.
      region: us-east-1
    # Labels to assign to the database, used in RBAC.
    labels:
      env: dev
auth_service:
  enabled: "no"
ssh_service:
  enabled: "no"
proxy_service:
  enabled: "no"
```

!!! tip

    A single Teleport process can run multiple different services, for example
    multiple database access proxies as well as running other services such an
    SSH service or an application access proxy.

Start the database service:

```sh
$ teleport start --debug --config=/path/to/teleport-db.yaml
```

### AWS Credentials

When setting up Teleport database service with AWS RDS or Aurora, it must have
an IAM role allowing it to connect to that particular database instance. An
example of such a policy is shown in the [AWS RDS/Aurora](#aws-rdsaurora-postgresql)
section above. See [Creating and using an IAM policy for IAM database access](https://docs.aws.amazon.com/AmazonRDS/latest/UserGuide/UsingWithRDS.IAMDBAuth.IAMPolicy.html)
in AWS documentation.

Teleport database service uses the default credential provider chain to find AWS
credentials. See [Specifying Credentials](https://docs.aws.amazon.com/sdk-for-go/v1/developer-guide/configuring-sdk.html#specifying-credentials)
for more information.

## Connect

Once the database service has joined the cluster, login to see the available
databases:

```sh
$ tsh login --proxy=teleport.example.com:3080
$ tsh db ls
Name   Description Labels
------ ----------- --------
aurora AWS Aurora  env=dev
```

Note that you will only be able to see databases your role has access to. See
[RBAC](#rbac) section for more details.

To connect to a particular database server, first retrieve credentials from
Teleport using `tsh db login` command:

```sh
$ tsh db login aurora
```

!!! tip

    You can be logged into multiple databases simultaneously.

You can optionally specify the database name and the user to use by default
when connecting to the database instance:

```sh
$ tsh db login --db-user=postgres --db-name=postgres aurora
```

When logging into a PostgreSQL database, `tsh` automatically configures a section
in the [connection service file](https://www.postgresql.org/docs/current/libpq-pgservice.html)
with the name of `<cluster-name>-<database-service-name>`.

Suppose the cluster name is "root", then you can connect to the database using
the following `psql` command:

```sh
# Use default database user/name.
$ psql "service=root-aurora"
# Specify database name/user explicitly.
$ psql "service=root-aurora user=alice dbname=metrics"
```

To log out of the database and remove credentials:

```sh
# Log out of a particular database instance.
$ tsh db logout aurora
# Log out of all database instances.
$ tsh db logout
```

## RBAC

Teleport's "role" resource provides the following instruments for restricting
database access:

```yaml
kind: role
version: v3
metadata:
  name: developer
spec:
  allow:
    # Label selectors for database instances this role has access to. These
    # will be matched against the static/dynamic labels set on the database
    # service.
    db_labels:
      environment: ["dev", "stage"]
    # Database names this role will be able to connect to. Note: this is not
    # the same as the "name" field in "db_service", this is the database names
    # within a particular database instance.
    db_names: ["main", "metrics", "postgres"]
    # Database users this role can connect as.
    db_users: ["alice", "bob"]
```

It is possible to use wildcards to match any database names/users. For example,
the following role permits access to any database/user within a production
database except for the internal "postgres" database/user:

```yaml
kind: role
version: v3
metadata:
  name: developer
spec:
  allow:
    db_labels:
      environment: ["dev", "prod"]
    db_names: ["*"]
    db_users: ["*"]
  deny:
    db_labels:
      environment: ["dev", "prod"]
    db_names: ["postgres"]
    db_users: ["postgres"]
```

Similar to other role fields, these support templating variables to allow
propagating information from identity providers:

```yaml
spec:
  allow:
    db_names: ["{% raw %}{{internal.db_names}}{% endraw %}", "{% raw %}{{external.xxx}}{% endraw %}"]
    db_users: ["{% raw %}{{internal.db_users}}{% endraw %}", "{% raw %}{{external.yyy}}{% endraw %}"]
```

See general [RBAC](../enterprise/ssh-rbac.md) documentation for more details.

## FAQ

### Which database protocols does Teleport Database Access support?

Currently Teleport Database Access Preview supports only PostgreSQL.

MySQL support is planned for [Teleport 6.0 release](./upcoming-releases.md#release-schedule),
with other databases to follow over the course of 2021 and beyond.

### Which PostgreSQL protocol features are not supported?

The following PostgreSQL protocol features aren't currently supported:

* [Canceling requests in progress](https://www.postgresql.org/docs/current/protocol-flow.html#id-1.10.5.7.9).
  Cancel requests issued by the PostgreSQL clients connected to Teleport proxy
  won't be passed to the database server.
* Any of the [authentication methods](https://www.postgresql.org/docs/current/auth-methods.html)
  except for client certificate authentication.

## RFD

Please refer to the [RFD document](https://github.com/gravitational/teleport/blob/024fd582e62560ffc925cd4b4d42c156043c041b/rfd/0011-database-access.md)
for a more in-depth description of the feature scope and design.

## Feedback

We value your feedback. Please schedule a Zoom call with us to get in-depth
demo and give us feedback using [this](https://calendly.com/benarent/teleport-database-access?month=2020-11)
link.

If you found a bug, please create a [Github
Issue](https://github.com/gravitational/teleport/issues/new/choose).
