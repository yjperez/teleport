---
title: Teleport HA mode on AWS
description: How to configure Teleport in highly available (HA) mode for AWS deployments
---

# Running Teleport Enterprise in HA mode on AWS

This guide is designed to accompany our [reference Terraform code](https://github.com/gravitational/teleport/tree/master/examples/aws/terraform/ha-autoscale-cluster#terraform-based-provisioning-example-amazon-single-ami)
and describe how to use and administrate the resulting Teleport deployment.

## Prerequisites

Our code requires Terraform 0.12+. You can [download Terraform here](https://www.terraform.io/downloads.html). We will assume that you have
`terraform` installed and available on your path.

```bash
$ which terraform
/usr/local/bin/terraform
$ terraform version
Terraform v0.12.20
```

You will also require the `aws` command line tool. This is available in Ubuntu/Debian/Fedora/CentOS and MacOS Homebrew
as the `awscli` package.

Fedora/CentOS: `yum -y install awscli`

Ubuntu/Debian: `apt-get -y install awscli`

MacOS (with [Homebrew](https://brew.sh/)): `brew install awscli`

When possible, installing via a package is always preferable. If you can't find a package available for
your distribution, you can also download the tool from [https://aws.amazon.com/cli/](https://aws.amazon.com/cli/)

We will assume that you have configured your AWS cli access with credentials available at `~/.aws/credentials`:

```bash
$ cat ~/.aws/credentials
[default]
aws_access_key_id = AKIA....
aws_secret_access_key = 8ZRxy....
```

You should also have a default region set under `~/.aws/config`:

```bash
$ cat ~/.aws/config
[default]
region = us-east-1
```

As a result, you should be able to run a command like `aws ec2 describe-instances` to list running EC2 instances.
If you get an "access denied", "403 Forbidden" or similar message, you will need to grant additional permissions to the
AWS IAM user that your `aws_access_key_id` and `aws_secret_access_key` refers to.

As a general rule, we assume that any user running Terraform has administrator-level permissions for the following
AWS services:

- [EC2](https://aws.amazon.com/ec2/)
- [S3](https://aws.amazon.com/s3/)
- [Route 53](https://aws.amazon.com/route53/)
- [DynamoDB](https://aws.amazon.com/dynamodb/)
- [Elastic Load Balancing](https://aws.amazon.com/elasticloadbalancing/)
- [IAM](https://aws.amazon.com/iam/)
- [SSM Parameter Store](https://docs.aws.amazon.com/systems-manager/latest/userguide/systems-manager-parameter-store.html)

The Terraform deployment itself will create new IAM roles to be used by Teleport instances which have appropriately
limited permission scopes for AWS services. However, the initial cluster setup must be done by a user with a high
level of AWS permissions.


## Get the Terraform code

Firstly, you'll need to clone the Teleport repo to get the Terraform code available on your system:

```bash
$ git clone https://github.com/gravitational/teleport
Cloning into 'teleport'...
remote: Enumerating objects: 106, done.
remote: Counting objects: 100% (106/106), done.
remote: Compressing objects: 100% (95/95), done.
remote: Total 61144 (delta 33), reused 35 (delta 11), pack-reused 61038
Receiving objects: 100% (61144/61144), 85.17 MiB | 4.66 MiB/s, done.
Resolving deltas: 100% (39141/39141), done.
```

Once this is done, you can change into the directory where the Terraform code is checked out and run `terraform init`:

```bash
$ cd teleport/examples/aws/terraform/ha-autoscale-cluster
$ terraform init

Initializing the backend...

Initializing provider plugins...
- Checking for available provider plugins...
- Downloading plugin for provider "template" (hashicorp/template) 2.1.2...
- Downloading plugin for provider "aws" (hashicorp/aws) 2.51.0...
- Downloading plugin for provider "random" (hashicorp/random) 2.2.1...

Terraform has been successfully initialized!

You may now begin working with Terraform. Try running "terraform plan" to see
any changes that are required for your infrastructure. All Terraform commands
should now work.

If you ever set or change modules or backend configuration for Terraform,
rerun this command to reinitialize your working directory. If you forget, other
commands will detect it and remind you to do so if necessary.
```

This will download the appropriate Terraform plugins needed to spin up Teleport using our
reference code.

## Set up variables

Terraform modules use variables to pass in input. You can do this in a few ways:

- on the command line to `terraform apply`
- by editing the `vars.tf` file
- by setting environment variables

For this guide, we are going to make extensive use of environment variables. This is because it makes it easier for us
to reference values from our configuration when running Teleport commands after the cluster has been created.

Any set environment variable starting with `TF_VAR_` is automatically processed and stripped down by Terraform, so
`TF_VAR_test_variable` becomes `test_variable`.

We maintain an up-to-date list of the variables and what they do in the README.md file under [the
`examples/aws/terraform/ha-autoscale-cluster` section of the Teleport repo](https://github.com/gravitational/teleport/blob/master/examples/aws/terraform/ha-autoscale-cluster/README.md)
but we'll run through an example list here.

Things you will need to decide on:

### region

Setting `export TF_VAR_region="us-west-2"`

The AWS region to run in. You should pick from the supported list as detailed in the [README](https://github.com/gravitational/teleport/blob/master/examples/aws/terraform/ha-autoscale-cluster/README.md). These are regions which support [DynamoDB encryption
at rest](https://docs.aws.amazon.com/amazondynamodb/latest/developerguide/EncryptionAtRest.html).


### cluster_name

Setting `export TF_VAR_cluster_name="example-cluster"`

This is the internal Teleport cluster name to use. This should be unique, and not contain spaces, dots (.) or other
special characters. Some AWS services will not allow you to use dots in a name, so this should not be set to a domain
name. This will appear in the web UI for your cluster and cannot be changed after creation without rebuilding your
cluster from scratch, so choose carefully.


### ami_name

Setting `export TF_VAR_ami_name="gravitational-teleport-ami-ent-{{ teleport.version }}"`

Gravitational automatically builds and publishes OSS, Enterprise and Enterprise FIPS 140-2 AMIs when we
release a new version of Teleport. The AMI names follow the format: `gravitational-teleport-ami-<type>-<version>`
where `<type>` is either `oss` or `ent` (Enterprise) and `version` is the version of Teleport e.g. `{{ teleport.version }}`.

FIPS 140-2 compatible AMIs (which deploy Teleport in FIPS 140-2 mode by default) have the `-fips` suffix.

The AWS account ID which publishes these AMIs is `126027368216`. You can list the available AMIs with
the example `awscli` commands below. The output is in JSON format by default.

!!! tip "List Gravitational AMIs"

    OSS AMIs<br />
    `aws ec2 describe-images --owners 126027368216 --filters 'Name=name,Values=gravitational-teleport-ami-oss*'`

    Enterprise AMIs<br />
    `aws ec2 describe-images --owners 126027368216 --filters 'Name=name,Values=gravitational-teleport-ami-ent*'`

    List Enterprise FIPS 140-2 AMIs<br />
    `aws ec2 describe-images --owners 126027368216 --filters 'Name=name,Values=gravitational-teleport-ami-ent*-fips'`


### key_name

Setting `export TF_VAR_key_name="exampleuser"`

The AWS keypair name to use when deploying EC2 instances. This must exist in the same region as you
specify in the `region` variable, and you will need a copy of this keypair available to connect to the deployed
EC2 instances. Do not use a keypair that you do not have access to.


### license_path

Setting `export TF_VAR_license_path="/home/user/teleport-license.pem"`

The full local path to your Teleport license file, which customers can download from
[the Gravitational dashboard](https://dashboard.gravitational.com/).

This license will be uploaded to AWS SSM and automatically downloaded to Teleport auth nodes in order to enable
Teleport Enterprise/Pro functionality.

(OSS users can provide any valid local file path here - it isn't used by the auth server in a Teleport OSS install)


### route53_zone

Setting `export TF_VAR_route53_zone="example.com"`

Our Terraform setup requires you to have your domain provisioned in AWS Route 53 - it will automatically add
DNS records for [`route53_domain`](#route53_domain) as set up below. You can list these with this command:

```bash
$ aws route53 list-hosted-zones --query "HostedZones[*].Name" --output text
[
    "example.com.",
    "testing.net.",
    "subdomain.wow.org."
]
```

You should use the appropriate domain without the trailing dot.


### route53_domain

Setting `export TF_VAR_route53_domain="teleport.example.com"`

A subdomain to set up as a CNAME to the Teleport load balancer for web access. This will be the public-facing domain
that people use to connect to your Teleport cluster, so choose wisely.

This must be a subdomain of the domain you chose for [`route53_zone`](#route53_zone) above.


### s3_bucket_name

Setting `export TF_VAR_s3_bucket_name="example-cluster"`

The Terraform example also provisions an S3 bucket to hold certificates provisioned by LetsEncrypt and distribute these
to EC2 instances. This can be any S3-compatible name, and will be generated in the same region as set above.

This bucket is still provisioned when using ACM, as it is also used to store Teleport session logs.


### email

Setting `export TF_VAR_email="support@example.com"`

LetsEncrypt requires an email address for every certificate registered which can be used to send notifications and
useful information. We recommend a generic ops/support email address which the team deploying Teleport has access to.


### grafana_pass

Setting `export TF_VAR_grafana_pass="CHANGE_THIS_VALUE"`

We deploy Grafana along with every Terraform deployment and automatically make stats on cluster usage available in
a custom dashboard. This variable sets up the password for the Grafana `admin` user. The Grafana web UI is served
on the same subdomain as specified above in [`route53_domain`](#route53_domain) on port 8443.

With the variables set in this example, it would be available on https://teleport.example.com:8443

If you do not change this from the default (`CHANGE_THIS_VALUE`), then it will be set to a random value for security
and you will need to log into the monitoring instance to discover this manually. As such, we recommend setting this
to a known value at the outset.


### use_acm

Setting `export TF_VAR_use_acm="false"`

If set to the string `"false"`, Terraform will use [LetsEncrypt](https://letsencrypt.org/) to provision the public-facing
web UI certificate for the Teleport cluster ([`route53_domain`](#route53_domain) - so https://teleport.example.com in this example).
This uses an [AWS network load balancer](https://docs.aws.amazon.com/elasticloadbalancing/latest/network/introduction.html)
to load-balance connections to the Teleport cluster's web UI, and its SSL termination is handled by Teleport itself.

If set to the string `"true"`, Terraform will use [AWS ACM](https://aws.amazon.com/certificate-manager/) to
provision the public-facing web UI certificate for the cluster. This uses an [AWS application load balancer](https://docs.aws.amazon.com/elasticloadbalancing/latest/application/introduction.html) to load-balance connections to the Teleport cluster's web UI, and its SSL termination is handled by the load balancer.

If you wish to use a pre-existing ACM certificate rather than having Terraform generate one for you, you can make
Terraform use it by running this command before `terraform apply`:

```bash
terraform import aws_acm_certificate.cert <certificate_arn>
```

## Reference deployment defaults

### Instances

Our reference deployment will provision the following instances for your cluster by default:

- 2 x m4.large Teleport **auth** instances in an ASG, behind an internal network load balancer, configured using DynamoDB for
shared storage. [The desired size of the ASG is configured here](https://github.com/gravitational/teleport/blob/master/examples/aws/terraform/ha-autoscale-cluster/auth_asg.tf#L11)
- 2 x m4.large Teleport **proxy** instances in an ASG, behind a public-facing load balancer - NLB for LetsEncrypt, ALB for ACM. [The desired size of the ASG is configured here](https://github.com/gravitational/teleport/blob/master/examples/aws/terraform/ha-autoscale-cluster/proxy_asg.tf#L12)
- 1 x m4.large Teleport **node** instance in an ASG. [The desired size of the ASG is configured here](https://github.com/gravitational/teleport/blob/master/examples/aws/terraform/ha-autoscale-cluster/node_asg.tf#L10)
- 1 x m4.large monitoring server in an ASG which hosts the Grafana instance and receives monitoring data from each service in the cluster. [The desired size of the ASG is configured here](https://github.com/gravitational/teleport/blob/master/examples/aws/terraform/ha-autoscale-cluster/monitor_asg.tf#L12)
- 1 x t2.medium bastion server which is the only permitted source for inbound SSH traffic to the instances. This is done
to avoid exposing each instance to the internet directly.

[The instance types used for each ASG can be configured here](https://github.com/gravitational/teleport/blob/master/examples/aws/terraform/ha-autoscale-cluster/vars.tf#L23-L45)

If you don't wish to set up a node or the monitoring services, you can set the `desired_size` and `min_size` for an ASG
to `0` and Terraform will not provision it.


### Cluster state database storage

The reference Terraform deployment sets Teleport up to store its cluster state database in DynamoDB. The name of the
table for cluster state will be the same as the cluster name configured in the [`cluster_name`](#cluster_name) variable above.

In our example, the DynamoDB table would be called `example-cluster`.

More information about how Teleport works with DynamoDB can be found in our [DynamoDB Admin Guide](https://gravitational.com/teleport/docs/admin-guide/#using-dynamodb).


### Audit event storage

The reference Terraform deployment sets Teleport up to store cluster audit logs in DynamoDB. The name of the table for
audit event storage will be the same as the cluster name configured in the [`cluster_name`](#cluster_name) variable above
with `-events` appended to the end.

In our example, the DynamoDB table would be called `example-cluster-events`.

More information about how Teleport works with DynamoDB can be found in our [DynamoDB Admin Guide](https://gravitational.com/teleport/docs/admin-guide/#using-dynamodb).


### Recorded session storage

The reference Terraform deployment sets Teleport up to store recorded session logs in the same S3 bucket configured in
the [`s3_bucket_name`](#s3_bucket_name) variable, under the `records` directory.

In our example this would be `s3://example-cluster/records`

!!! tip "Tip"

    S3 provides [Amazon S3 Object Lock](https://docs.aws.amazon.com/AmazonS3/latest/dev/object-lock.html),
    which is useful for customers deploying Teleport in regulated environments. Configuration of object lock is out of
    the scope of this guide.


### Cluster domain

The reference Terraform deployment sets the Teleport cluster up to be available on a domain defined in Route53, referenced
by the [`route53_domain`](#route53_domain) variable. In our example this would be `teleport.example.com`

Teleport's web interface will be available on port 443 - https://teleport.example.com - this is via a configured CNAME
to the AWS load balancer.

Teleport's proxy SSH interface will be available via a network load balancer with an AWS-controlled hostname on port 3023.
This is the default port used when connecting with the `tsh` client and will not require any additional configuration.

Teleport's tunnel interface will be available via the same network load balancer with an AWS-controlled hostname on port
3024. This allows trusted clusters and nodes connected via node tunnelling to access the cluster.

After deploying, you can get the hostname of the proxy/tunnel network load balancer if needed with this command:

```bash
$ aws elbv2 describe-load-balancers --names "${TF_VAR_cluster_name}-proxy" --query "LoadBalancers[*].DNSName" --output text
example-cluster-proxy-7c97b76593d6bf21.elb.us-east-1.amazonaws.com
```

Teleport's auth server interface will be available via an internal load balancer with an AWS-controlled hostname on port 3025.

After deploying, you can get the hostname of the internal auth load balancer if needed with this command:

```bash
$ aws elbv2 describe-load-balancers --names "${TF_VAR_cluster_name}-auth" --query "LoadBalancers[*].DNSName" --output text
example-cluster-auth-c5b0fc2764ee015b.elb.us-east-1.amazonaws.com
```


## Deploying with Terraform

Once you have set values for and exported all the variables detailed above, you should run `terraform plan` to validate the
configuration.

```bash
$ terraform plan
Refreshing Terraform state in-memory prior to plan...
The refreshed state will be used to calculate this plan, but will not be
persisted to local or remote state storage.

data.template_file.monitor_user_data: Refreshing state...
data.aws_kms_alias.ssm: Refreshing state...
data.aws_caller_identity.current: Refreshing state...
data.aws_ami.base: Refreshing state...
data.aws_availability_zones.available: Refreshing state...
data.aws_route53_zone.proxy: Refreshing state...
data.aws_region.current: Refreshing state...

------------------------------------------------------------------------

An execution plan has been generated and is shown below.
Resource actions are indicated with the following symbols:
  + create
 <= read (data resources)

Terraform will perform the following actions:
  <output trimmed>

Plan: 121 to add, 0 to change, 0 to destroy.

------------------------------------------------------------------------

Note: You didn't specify an "-out" parameter to save this plan, so Terraform
can't guarantee that exactly these actions will be performed if
"terraform apply" is subsequently run.
```

This looks good (no errors produced by Terraform) so we can run `terraform apply`:

```bash
$ terraform apply
  <output trimmed>

Plan: 121 to add, 0 to change, 0 to destroy.

Do you want to perform these actions?
  Terraform will perform the actions described above.
  Only 'yes' will be accepted to approve.

  Enter a value:
```

Entering `yes` here will start the Terraform deployment. It takes around 8-10 minutes to deploy in full.


#### Destroy/shutdown a Terraform deployment

If you need to tear down a running deployment for any reason, you can run `terraform destroy`.


## Accessing the cluster after Terraform setup

Once the Terraform setup is finished, your Teleport cluster's web UI should be available on
https://[route53_domain](#route53_domain) - this is https://teleport.example.com in our example.


### Adding an admin user to the Teleport cluster

To add users to the Teleport cluster, you will need to connect to a Teleport auth server via SSH and run the `tctl` command.

1 - Use the AWS cli to get the IP of the bastion server:

```bash
$ export BASTION_IP=$(aws ec2 describe-instances --filters "Name=tag:TeleportCluster,Values=${TF_VAR_cluster_name},Name=tag:TeleportRole,Values=bastion" --query "Reservations[*].Instances[*].PublicIpAddress" --output text)
$ echo ${BASTION_IP}
1.2.3.4
```

2 - Use the AWS cli to get the IP of an auth server:

```bash
$ export AUTH_IP=$(aws ec2 describe-instances --filters "Name=tag:TeleportCluster,Values=${TF_VAR_cluster_name},Name=tag:TeleportRole,Values=auth" --query "Reservations[0].Instances[*].PrivateIpAddress" --output text)
$ echo ${AUTH_IP}
172.31.0.196
```

3 - Use both these values to SSH into the auth server. Make sure that the AWS keypair that you specified in the
[`key_name`](#key_name) variable is available in the current directory, or update the `-i` parameter to point to it:

```bash
$ ssh -i ${TF_VAR_key_name}.pem -o ProxyCommand="ssh -i ${TF_VAR_key_name}.pem -W '[%h]:%p' ec2-user@${BASTION_IP}" ec2-user@${AUTH_IP}
The authenticity of host '1.2.3.4 (1.2.3.4)' can't be established.
ECDSA key fingerprint is SHA256:vFPnCFliRsRQ1Dk+muIv2B1Owm96hXiihlOUsj5H3bg.
Are you sure you want to continue connecting (yes/no/[fingerprint])? yes
Warning: Permanently added '1.2.3.4' (ECDSA) to the list of known hosts.
The authenticity of host '172.31.0.196 (<no hostip for proxy command>)' can't be established.
ECDSA key fingerprint is SHA256:vFPnCFliRsRQ1Dk+muIv2B1Owm96hXiihlOUsj5H3bg.
Are you sure you want to continue connecting (yes/no/[fingerprint])? yes
Warning: Permanently added '172.31.0.196' (ECDSA) to the list of known hosts.
Last login: Tue Mar  3 18:57:12 2020 from 1.2.3.5

       __|  __|_  )
       _|  (     /   Amazon Linux 2 AMI
      ___|\___|___|

https://aws.amazon.com/amazon-linux-2/
1 package(s) needed for security, out of 6 available
Run "sudo yum update" to apply all updates.
[ec2-user@ip-172-31-0-196 ~]$
```

4 - Use the `tctl` command to create an admin user for Teleport:

```bash
[ec2-user@ip-172-31-0-196 ~]$ sudo tctl users add teleport-admin --roles=admin
Signup token has been created and is valid for 1 hours. Share this URL with the user:
https://teleport.example.com:443/web/newuser/6489ae886babf4232826076279bcb2fb

NOTE: Make sure teleport.example.com:443 points at a Teleport proxy which users can access.
When the user 'teleport-admin' activates their account, they will be assigned roles [admin]
```

5 - Click the link to launch the Teleport web UI and finish setting up your user. You will need to scan the QR
code with an TOTP-compatible app like Google Authenticator or Authy. You will also set a password for the
`teleport-admin` user on this page.

Once this user is successfully configured, you should be logged into the Teleport web UI.


### Logging into the cluster with tsh

You can use the Teleport command line tool (`tsh`) to log into your Teleport cluster after provisioning a user.

You can [download the Teleport package containing the `tsh` client from here](https://gravitational.com/teleport/download)
- the client is the same for both OSS and Enterprise versions of Teleport.

```bash
$ tsh login --proxy=${TF_VAR_route53_domain}:443 --user=teleport-admin
Enter password for Teleport user teleport-admin:
Enter your OTP token:
567989
> Profile URL:  https://teleport.example.com:443
  Logged in as: teleport-admin
  Cluster:      example-cluster
  Roles:        admin*
  Logins:       root
  Valid until:  2020-03-06 22:07:11 -0400 AST [valid for 12h0m0s]
  Extensions:   permit-agent-forwarding, permit-port-forwarding, permit-pty


* RBAC is only available in Teleport Enterprise
  https://gravitational.com/teleport/docs/enterprise

$ tsh ls
Node Name                    Address           Labels
---------------------------- ----------------- ------
ip-172-31-11-69-ec2-internal 172.31.11.69:3022

$ tsh ssh root@ip-172-31-11-69-ec2-internal
[root@ip-172-31-11-69 ~]#
```


## Restarting/checking Teleport services

### LetsEncrypt

!!! note

    You are using LetsEncrypt if your `use_acm` variable is set to `"false"`.

#### Auth service

```bash
$ systemctl status teleport-auth.service
● teleport-auth.service - Teleport Auth Service
   Loaded: loaded (/etc/systemd/system/teleport-auth.service; enabled; vendor preset: disabled)
   Active: active (running) since Thu 2020-03-05 16:45:18 UTC; 4h 14min ago
 Main PID: 3766 (teleport)
   CGroup: /system.slice/teleport-auth.service
           └─3766 /usr/bin/teleport start --config=/etc/teleport.yaml --diag-addr=127.0.0.1:3434 --pid-file=/run/teleport/teleport.pid

Mar 05 17:54:58 ip-172-31-0-196.ec2.internal /usr/bin/teleport[3766]: INFO [CA]        Generating TLS certificate {0x3767920 0xc0012802f0 CN=teleport-admin,O=admin,POSTALCODE={\"kubernetes_groups\":null\,\"logins\":null},STREET=,L=root 2020-03-06 05:54:58.846233666 +0000 UTC []}. common_name:teleport-admin dns_name...
Mar 05 18:04:39 ip-172-31-0-196.ec2.internal /usr/bin/teleport[3766]: INFO [CA]        Generating TLS certificate {0x3767920 0xc00155d200 CN=teleport-admin,O=admin,POSTALCODE={\"kubernetes_groups\":null\,\"logins\":null},STREET=,L=root 2020-03-06 06:04:39.844777551 +0000 UTC []}. common_name:teleport-admin dns_name...
```

You can get detailed logs for the Teleport auth servers using the `journalctl` command:

```bash
$ journalctl -u teleport-auth.service
```

Remember that there is more than one auth server in an HA deployment. You should use this command to get the IP addresses
of each auth server that you'll need to connect to:

```bash
$ aws ec2 describe-instances --filters "Name=tag:TeleportCluster,Values=${TF_VAR_cluster_name},Name=tag:TeleportRole,Values=auth" --query "Reservations[*].Instances[*].PrivateIpAddress" --output text
172.31.0.196
172.31.1.78
```

You can run `tctl` commands on **any** of the auth instances connected to your cluster, however.

#### Proxy service

```bash
$ systemctl status teleport-proxy.service
● teleport-proxy.service - Teleport Proxy Service
   Loaded: loaded (/etc/systemd/system/teleport-proxy.service; enabled; vendor preset: disabled)
   Active: active (running) since Thu 2020-03-05 17:14:37 UTC; 3h 47min ago
  Process: 4502 ExecStartPre=/usr/bin/teleport-ssm-get-token (code=exited, status=0/SUCCESS)
 Main PID: 4514 (teleport)
   CGroup: /system.slice/teleport-proxy.service
           └─4514 /usr/bin/teleport start --config=/etc/teleport.yaml --diag-addr=127.0.0.1:3434 --pid-file=/run/teleport/teleport.pid

Mar 05 20:58:25 ip-172-31-2-109.ec2.internal /usr/bin/teleport[4514]: ERRO             read tcp 172.31.2.109:3024->172.31.2.143:1577: read: connection reset by peer
Mar 05 20:58:50 ip-172-31-2-109.ec2.internal /usr/bin/teleport[4514]: ERRO             read tcp 172.31.2.109:3023->172.31.2.143:38011: read: connection reset by peer
```

You can get detailed logs for the Teleport proxy service using the `journalctl` command:

```bash
$ journalctl -u teleport-proxy.service
```

Remember that there is more than one proxy instance in an HA deployment. You should use this command to get the IP addresses
of each auth instance that you'll need to connect to:

```bash
$ aws ec2 describe-instances --filters "Name=tag:TeleportCluster,Values=${TF_VAR_cluster_name},Name=tag:TeleportRole,Values=proxy" --query "Reservations[*].Instances[*].PrivateIpAddress" --output text
172.31.2.109
172.31.3.215
```

#### Node service

```bash
$ systemctl status teleport-node.service
● teleport-node.service - Teleport SSH Node Service
   Loaded: loaded (/etc/systemd/system/teleport-node.service; enabled; vendor preset: disabled)
   Active: active (running) since Thu 2020-03-05 17:18:25 UTC; 3h 44min ago
  Process: 4444 ExecStartPre=/usr/bin/teleport-ssm-get-token (code=exited, status=0/SUCCESS)
 Main PID: 4456 (teleport)
   CGroup: /system.slice/teleport-node.service
           └─4456 /usr/bin/teleport start --config=/etc/teleport.yaml --diag-addr=127.0.0.1:3434 --pid-file=/run/teleport/teleport.pid

Mar 05 17:18:25 ip-172-31-11-69.ec2.internal /usr/bin/teleport[4456]: INFO [AUDIT:1]   Creating directory /var/lib/teleport/log/upload/sessions. ser...o:1630
Mar 05 17:18:25 ip-172-31-11-69.ec2.internal /usr/bin/teleport[4456]: INFO [AUDIT:1]   Setting directory /var/lib/teleport/log/upload/sessions owner...o:1639
```

You can get detailed logs for the Teleport node service using the `journalctl` command:

```bash
$ journalctl -u teleport-node.service
```

### ACM

!!! note

    You are using ACM if your `use_acm` variable is set to `"true"`.

When using ACM, the service name for the proxy is different (`teleport-proxy-acm.service` vs `teleport-proxy.service`).

#### Auth service

```bash
$ systemctl status teleport-auth.service
● teleport-auth.service - Teleport Auth Service
   Loaded: loaded (/etc/systemd/system/teleport-auth.service; enabled; vendor preset: disabled)
   Active: active (running) since Thu 2020-03-05 16:45:18 UTC; 4h 14min ago
 Main PID: 3766 (teleport)
   CGroup: /system.slice/teleport-auth.service
           └─3766 /usr/bin/teleport start --config=/etc/teleport.yaml --diag-addr=127.0.0.1:3434 --pid-file=/run/teleport/teleport.pid

Mar 05 17:54:58 ip-172-31-0-196.ec2.internal /usr/bin/teleport[3766]: INFO [CA]        Generating TLS certificate {0x3767920 0xc0012802f0 CN=teleport-admin,O=admin,POSTALCODE={\"kubernetes_groups\":null\,\"logins\":null},STREET=,L=root 2020-03-06 05:54:58.846233666 +0000 UTC []}. common_name:teleport-admin dns_name...
Mar 05 18:04:39 ip-172-31-0-196.ec2.internal /usr/bin/teleport[3766]: INFO [CA]        Generating TLS certificate {0x3767920 0xc00155d200 CN=teleport-admin,O=admin,POSTALCODE={\"kubernetes_groups\":null\,\"logins\":null},STREET=,L=root 2020-03-06 06:04:39.844777551 +0000 UTC []}. common_name:teleport-admin dns_name...
```

You can get detailed logs for the Teleport auth server using the `journalctl` command:

```bash
$ journalctl -u teleport-auth.service
```

Remember that there is more than one auth instance in an HA deployment. You should use this command to get the IP addresses
of each auth instance that you'd need to connect to for checking logs:

```bash
$ aws ec2 describe-instances --filters "Name=tag:TeleportCluster,Values=${TF_VAR_cluster_name},Name=tag:TeleportRole,Values=auth" --query "Reservations[*].Instances[*].PrivateIpAddress" --output text
172.31.0.196
172.31.1.78
```

You can run `tctl` commands on **any** of the auth instances connected to your cluster, however.

#### Proxy service (ACM)

```bash
$ systemctl status teleport-proxy-acm.service
● teleport-proxy-acm.service - Teleport Proxy Service (ACM)
   Loaded: loaded (/etc/systemd/system/teleport-proxy-acm.service; enabled; vendor preset: disabled)
   Active: active (running) since Thu 2020-03-05 17:14:37 UTC; 3h 47min ago
  Process: 4502 ExecStartPre=/usr/bin/teleport-ssm-get-token (code=exited, status=0/SUCCESS)
 Main PID: 4514 (teleport)
   CGroup: /system.slice/teleport-proxy-acm.service
           └─4514 /usr/bin/teleport start --config=/etc/teleport.yaml --diag-addr=127.0.0.1:3434 --pid-file=/run/teleport/teleport.pid

Mar 05 20:58:25 ip-172-31-2-109.ec2.internal /usr/bin/teleport[4514]: ERRO             read tcp 172.31.2.109:3024->172.31.2.143:1577: read: connection reset by peer
Mar 05 20:58:50 ip-172-31-2-109.ec2.internal /usr/bin/teleport[4514]: ERRO             read tcp 172.31.2.109:3023->172.31.2.143:38011: read: connection reset by peer
```

You can get detailed logs for the Teleport proxy service using the `journalctl` command:

```bash
$ journalctl -u teleport-proxy-acm.service
```

Remember that there is more than one proxy instance in an HA deployment. You can use this command to get the IP addresses
of each proxy instance that you'd need to connect to for checking logs:

```bash
$ aws ec2 describe-instances --filters "Name=tag:TeleportCluster,Values=${TF_VAR_cluster_name},Name=tag:TeleportRole,Values=proxy" --query "Reservations[*].Instances[*].PrivateIpAddress" --output text
172.31.2.109
172.31.3.215
```

#### Node service

```bash
$ systemctl status teleport-node.service
● teleport-node.service - Teleport SSH Node Service
   Loaded: loaded (/etc/systemd/system/teleport-node.service; enabled; vendor preset: disabled)
   Active: active (running) since Thu 2020-03-05 17:18:25 UTC; 3h 44min ago
  Process: 4444 ExecStartPre=/usr/bin/teleport-ssm-get-token (code=exited, status=0/SUCCESS)
 Main PID: 4456 (teleport)
   CGroup: /system.slice/teleport-node.service
           └─4456 /usr/bin/teleport start --config=/etc/teleport.yaml --diag-addr=127.0.0.1:3434 --pid-file=/run/teleport/teleport.pid

Mar 05 17:18:25 ip-172-31-11-69.ec2.internal /usr/bin/teleport[4456]: INFO [AUDIT:1]   Creating directory /var/lib/teleport/log/upload/sessions. ser...o:1630
Mar 05 17:18:25 ip-172-31-11-69.ec2.internal /usr/bin/teleport[4456]: INFO [AUDIT:1]   Setting directory /var/lib/teleport/log/upload/sessions owner...o:1639
```

You can get detailed logs for the Teleport node service using the `journalctl` command:

```bash
$ journalctl -u teleport-node.service
```

## Adding EC2 instances to your Teleport cluster
Customers run many workloads within EC2 and depending on how you work, there are many
ways to integrate Teleport onto your servers. We recommend looking at our [Admin manual](https://gravitational.com/teleport/docs/admin-guide/#installing).

To add new nodes/EC2 servers that you can "SSH into" you'll need to:

- [Install the Teleport binary on the Server](admin-guide.md#installing)
- [Run Teleport - we recommend using systemd](admin-guide.md#systemd-unit-file)
- [Set the correct settings in /etc/teleport.yaml](admin-guide.md#configuration-file)
- [Add nodes to the Teleport cluster](admin-guide.md#adding-nodes-to-the-cluster)


### Getting the CA pin hash

You can use this command to get the CA pin hash for your Teleport cluster:

```bash
$ aws ssm get-parameter --name "/teleport/${TF_VAR_cluster_name}/ca-pin-hash" --query "Parameter.Value" --output text
sha256:d021ef54aaf8633c4e15c5cc59479fb2f19b1bbc5432bb95213ee047000689dd
```

You should use this so that nodes can validate the auth server's identity when joining your cluster.


### Getting the node join token

You can use this command to get a join token for your Teleport cluster:

```bash
$ aws ssm get-parameter --name "/teleport/${TF_VAR_cluster_name}/tokens/node" --query "Parameter.Value" --output text
AQICAHgLq8feq4riNouuw8Wxs5EEPlS2qKIVE5Z/qEo1i6mqfwGX3dW56SdoS6PinTWbZL1RAAAAgzCBgAYJKoZIhvcNAQcGoHMwcQIBADBsBgkqhkiG9w0BBwEwHgYJYIZIAWUDBAEuMBEEDNdu5TxaT8gyJx63eAIBEIA/JEpX2Vte90UmufIzZzvBQcQaKgWr95aN9xZYMEjWbAiNitxkvZgb98FgFn8d9GNwKQgDGfUYDbzsX8EqTtx9
```

You should use this so that nodes can validate the auth server's identity when joining your cluster.

You can also generate a node join token using `tctl tokens add --type=node` [as detailed here in our admin guide](admin-guide.md#adding-nodes-to-the-cluster).


### Joining nodes via the Teleport auth server

To join Teleport nodes in the same VPC via the auth server, you can find the hostname for the auth load balancer with
this command:

```bash
$ aws elbv2 describe-load-balancers --names "${TF_VAR_cluster_name}-auth" --query "LoadBalancers[*].DNSName" --output text
example-cluster-auth-c5b0fc2764ee015b.elb.us-east-1.amazonaws.com
```

With this method, the nodes should be configured like so:

```yaml
auth_servers:
  - example-cluster-auth-c5b0fc2764ee015b.elb.us-east-1.amazonaws.com:3025
```


### Joining nodes via Teleport IoT/node tunnelling

To join Teleport nodes from outside the same VPC, you will either need to investigate VPC peering/gateways (out of scope
for this document) or join your nodes using [Teleport's node tunnelling](admin-guide.md#adding-a-node-located-behind-nat) functionality.

With this method, you can join the nodes using the public facing proxy address - `teleport.example.com:443` for our
example.

```yaml
auth_servers:
  - teleport.example.com:443
```


### Trusted clusters

To add a trusted cluster, you'll need the hostname of the proxy load balancer. You can get it using this command:

```bash
$ aws elbv2 describe-load-balancers --names "${TF_VAR_cluster_name}-proxy" --query "LoadBalancers[*].DNSName" --output text
example-cluster-proxy-7c97b76593d6bf21.elb.us-east-1.amazonaws.com
```

In this example, the `tunnel_addr` and `web_proxy_addr` in the trusted cluster configuration should be set up like this:

```yaml
spec:
  tunnel_addr: example-cluster-proxy-7c97b76593d6bf21.elb.us-east-1.amazonaws.com:3024
  web_proxy_addr: teleport.example.com:443
```

You can generate a token for adding the trusted cluster using `tctl tokens add --type=trusted_cluster` after connecting
to an auth server. Follow the instructions in our [trusted cluster guide](https://gravitational.com/teleport/docs/trustedclusters/#dynamic-join-tokens).


## Script to quickly connect to instances

Here's a bash script that you can use to quickly connect to instances:

```bash
#!/bin/bash
if [[ "$1" != "" ]]; then
    INSTANCE_TYPE=$1
else
    INSTANCE_TYPE="auth"
fi
if [[ "$2" != "" ]]; then
    INSTANCE_ID=$2
else
    INSTANCE_ID="0"
fi

export BASTION_IP=$(aws ec2 describe-instances --filters "Name=tag:TeleportCluster,Values=${TF_VAR_cluster_name},Name=tag:TeleportRole,Values=bastion" --query "Reservations[*].Instances[*].PublicIpAddress" --output text)
echo "Bastion IP: ${BASTION_IP}"

if [[ "${INSTANCE_TYPE}" == "auth" ]]; then
    export SERVER_IP=$(aws ec2 describe-instances --filters "Name=tag:TeleportCluster,Values=${TF_VAR_cluster_name},Name=tag:TeleportRole,Values=auth" --query "Reservations[${INSTANCE_ID}].Instances[*].PrivateIpAddress" --output text)
    echo "Auth ${INSTANCE_ID} IP: ${SERVER_IP}"
elif [[ "${INSTANCE_TYPE}" == "proxy" ]]; then
    export SERVER_IP=$(aws ec2 describe-instances --filters "Name=tag:TeleportCluster,Values=${TF_VAR_cluster_name},Name=tag:TeleportRole,Values=proxy" --query "Reservations[${INSTANCE_ID}].Instances[*].PrivateIpAddress" --output text)
    echo "Proxy ${INSTANCE_ID} IP: ${SERVER_IP}"
elif [[ "${INSTANCE_TYPE}" == "node" ]]; then
    export SERVER_IP=$(aws ec2 describe-instances --filters "Name=tag:TeleportCluster,Values=${TF_VAR_cluster_name},Name=tag:TeleportRole,Values=node" --query "Reservations[*].Instances[*].PrivateIpAddress" --output text)
    echo "Node IP: ${SERVER_IP}"
fi

echo "Keypair name: ${TF_VAR_key_name}"
ssh -i ${TF_VAR_key_name}.pem -o ProxyCommand="ssh -i ${TF_VAR_key_name}.pem -W '[%h]:%p' ec2-user@${BASTION_IP}" ec2-user@${SERVER_IP}
```

Save this as `connect.sh`, run `chmod +x connect.sh` to make it executable, then use it like so:

```bash
# connect to the first auth server
$ ./connect.sh auth 0

# connect to the second auth server
$ ./connect.sh auth 1

# connect to the first proxy server
$ ./connect.sh proxy 0

# connect to the second proxy server
$ ./connect.sh proxy 1

# connect to the node
$ ./connect.sh node
```
