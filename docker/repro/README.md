# Reproduction for [#4542](https://github.com/gravitational/teleport/issues/4542)

1) Add `teleport` to your `localhost` line in `/etc/hosts`

2) Create `data` directories:

```bash
$ mkdir -p teleport/data teleport-node/data
```

3) Start the containers:

```bash
$ docker-compose up -d
```

4) Log into the cluster (credentials are `testuser:testuser`):

```bash
$ tsh login --proxy=localhost:3080 --user=testuser --insecure
WARNING: You are using insecure connection to SSH proxy https://teleport:3080
Enter password for Teleport user testuser:
WARNING: You are using insecure connection to SSH proxy https://teleport:3080
> Profile URL:  https://teleport:3080
  Logged in as: testuser
  Cluster:      teleport
  Roles:        admin*
  Logins:       root
  Valid until:  2020-10-27 23:15:14 -0300 ADT [valid for 12h0m0s]
  Extensions:   permit-agent-forwarding, permit-port-forwarding, permit-pty


* RBAC is only available in Teleport Enterprise
  https://gravitational.com/teleport/docs/enterprise
```

5) Verify that you can see the node:

```bash
$ tsh ls

Node Name     Address    Labels 
------------- ---------- ------ 
teleport-node ⟵ Tunnel          
```

6) Connect to the node using `tsh`, verify that it works:

```bash
$ tsh ssh root@teleport-node
root@teleport-node:~#
```

7) Connect to the node using `ssh`, verify that it works (accept any SSH trust prompts that you need to):

```bash
$ ssh -vv -F ./ssh_config root@teleport-node
The authenticity of host '[localhost]:3023 ([::1]:3023)' can't be established.
RSA key fingerprint is SHA256:GYATf3Z+mLvcr9tpuTBRILqhZ6ela3q+3/mj2CQVvHY.
Are you sure you want to continue connecting (yes/no/[fingerprint])? yes
The authenticity of host '[teleport-node]:3022 (<no hostip for proxy command>)' can't be established.
RSA key fingerprint is SHA256:N0DW2GHq5XC/vXMiEpb1KAJRLJ11sCswWOtKOXpg7L0.
Are you sure you want to continue connecting (yes/no/[fingerprint])? yes
root@teleport-node:~#
```

8) Kill the docker container running the node:

```bash
$ docker kill teleport-node
```

9) Try to connect again via `tsh`, you should get an error:

```bash
$ tsh ssh root@teleport-node
error: failed connecting to node teleport-node. node is offline, please try again later
```

10) Try to connect again via `ssh`, you should see the error as described in [#4542](https://github.com/gravitational/teleport/issues/4542):

```bash
$ ssh -F ./ssh_config root@teleport-node
node is offline, please try again latersubsystem request failed on channel 0
kex_exchange_identification: Connection closed by remote host
```
