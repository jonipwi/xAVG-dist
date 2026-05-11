# xAVG Public Blacklist Client Setup

This guide shows how to use the standalone xAVG client to sync both public feeds into MikroTik address-lists:

- IPv4 feed -> `blacklist`
- CIDR feed -> `cidr_blacklist`

Public feeds:

```text
https://xavg.bixio.xyz/api/public/blacklist.json
https://xavg.bixio.xyz/api/public/cidr-blacklist.json
```

Expected payloads:

```json
{
  "ips": ["203.0.113.10"]
}
```

```json
{
  "cidrs": ["203.0.113.0/24"]
}
```

## What The Client Does

`dist/xavg-client.go`:

- Downloads two public JSON feeds (`ips` and `cidrs`)
- Reads `ips` for IPv4 list sync and `cidrs` for CIDR list sync
- Filters invalid, private, loopback, link-local, CGNAT, and multicast IPv4 data
- Connects to MikroTik over SSH
- Checks the existing RouterOS firewall address list
- Adds missing IPv4 entries to `/ip firewall address-list list=blacklist`
- Adds missing CIDR entries to `/ip firewall address-list list=cidr_blacklist`
- Runs immediately on startup, then repeats on the configured scheduler interval

It does not delete existing MikroTik entries.

## MikroTik Setup

Create or choose a RouterOS user that can read and update firewall address lists. SSH must be enabled on the MikroTik.

Check SSH service:

```routeros
/ip service print where name=ssh
```

Enable SSH if needed:

```routeros
/ip service enable ssh
```

For a dedicated client user, create a group with firewall permissions:

```routeros
/user group add name=xavg-policy policy=ssh,read,write
/user add name=xavg-client group=xavg-policy password=CHANGE_THIS_PASSWORD
```

The client will create address-list entries automatically. You can verify them with:

```routeros
/ip firewall address-list print where list=blacklist
/ip firewall address-list print where list=cidr_blacklist
```

## Firewall Rule

Add firewall rules that drop traffic from both address-lists. Put these rules near the top of the relevant chain, before broad allow rules.

For blocking inbound WAN traffic to the router:

```routeros
/ip firewall filter add chain=input src-address-list=blacklist action=drop comment="drop xAVG blacklist to router"
/ip firewall filter add chain=input src-address-list=cidr_blacklist action=drop comment="drop xAVG cidr blacklist to router"
```

For blocking forwarded traffic through the router:

```routeros
/ip firewall filter add chain=forward src-address-list=blacklist action=drop comment="drop xAVG blacklist through router"
/ip firewall filter add chain=forward src-address-list=cidr_blacklist action=drop comment="drop xAVG cidr blacklist through router"
```

Move the rule near the top if needed:

```routeros
/ip firewall filter move [find comment="drop xAVG blacklist to router"] destination=0
/ip firewall filter move [find comment="drop xAVG blacklist through router"] destination=0
/ip firewall filter move [find comment="drop xAVG cidr blacklist to router"] destination=0
/ip firewall filter move [find comment="drop xAVG cidr blacklist through router"] destination=0
```

If your router uses interface lists, you can restrict the rule to WAN traffic:

```routeros
/ip firewall filter add chain=input in-interface-list=WAN src-address-list=blacklist action=drop comment="drop xAVG blacklist from WAN"
/ip firewall filter add chain=forward in-interface-list=WAN src-address-list=blacklist action=drop comment="drop xAVG blacklist forward from WAN"
/ip firewall filter add chain=input in-interface-list=WAN src-address-list=cidr_blacklist action=drop comment="drop xAVG cidr blacklist from WAN"
/ip firewall filter add chain=forward in-interface-list=WAN src-address-list=cidr_blacklist action=drop comment="drop xAVG cidr blacklist forward from WAN"
```

## Client Configuration

Create `dist/.env` next to the executable:

```ini
XAVG_BLACKLIST_URL=https://xavg.bixio.xyz/api/public/blacklist.json
XAVG_CIDR_BLACKLIST_URL=https://xavg.bixio.xyz/api/public/cidr-blacklist.json

MT_HOST=192.168.88.1:22
MT_USER=xavg-client
MT_PASS=CHANGE_THIS_PASSWORD

MT_BLACKLIST_LIST=blacklist
MT_BLACKLIST_COMMENT=xavg-public-blacklist
MT_CIDR_BLACKLIST_LIST=cidr_blacklist
MT_CIDR_BLACKLIST_COMMENT=xavg-public-cidr-blacklist

SCHEDULER=1h
```

Notes:

- `MT_HOST` must include the SSH port, for example `192.168.88.1:22`
- `MT_USER` and `MT_PASS` are the MikroTik SSH credentials
- `XAVG_BLACKLIST_URL` is the IPv4 feed URL (`ips`)
- `XAVG_CIDR_BLACKLIST_URL` is the CIDR feed URL (`cidrs`)
- `MT_BLACKLIST_LIST=blacklist` should match IPv4 firewall drop rules
- `MT_CIDR_BLACKLIST_LIST=cidr_blacklist` should match CIDR firewall drop rules
- `MT_BLACKLIST_COMMENT` and `MT_CIDR_BLACKLIST_COMMENT` are written on new entries
- `SCHEDULER` is a Go duration such as `1h`, `30m`, or `15m`; the default is `1h`

You can also pass values as flags:

```powershell
.\dist\xavg-client.exe -mt-host 192.168.88.1:22 -mt-user xavg-client -mt-pass CHANGE_THIS_PASSWORD -url-ip https://xavg.bixio.xyz/api/public/blacklist.json -url-cidr https://xavg.bixio/api/public/cidr-blacklist.json -list-ip blacklist -list-cidr cidr_blacklist
```

## Run

Test without writing to MikroTik. The client runs immediately, then repeats on the scheduler interval until stopped:

```powershell
.\dist\xavg-client.exe -dry-run
```

Run the scheduled sync:

```powershell
.\dist\xavg-client.exe
```

On macOS or Linux:

```bash
./dist/xavg-client -dry-run
./dist/xavg-client
```

## Build Requirements

Install Go from:

```text
https://go.dev/dl/
```

This project uses Go modules. Build commands should be run from the repository root.

## Build On Windows

PowerShell:

```powershell
go mod tidy
go build -o .\dist\xavg-client.exe .\dist\xavg-client.go
```

Run:

```powershell
.\dist\xavg-client.exe -dry-run
.\dist\xavg-client.exe
```

## Build On macOS

Terminal:

```bash
go mod tidy
go build -o ./dist/xavg-client ./dist/xavg-client.go
chmod +x ./dist/xavg-client
```

Run:

```bash
./dist/xavg-client -dry-run
./dist/xavg-client
```

## Build On Linux

Terminal:

```bash
go mod tidy
go build -o ./dist/xavg-client ./dist/xavg-client.go
chmod +x ./dist/xavg-client
```

Run:

```bash
./dist/xavg-client -dry-run
./dist/xavg-client
```

## Cross Compile

From Windows, build Linux and macOS binaries:

```powershell
$env:GOOS="linux"; $env:GOARCH="amd64"; go build -o .\dist\xavg-client-linux-amd64 .\dist\xavg-client.go
$env:GOOS="darwin"; $env:GOARCH="amd64"; go build -o .\dist\xavg-client-darwin-amd64 .\dist\xavg-client.go
$env:GOOS="darwin"; $env:GOARCH="arm64"; go build -o .\dist\xavg-client-darwin-arm64 .\dist\xavg-client.go
$env:GOOS="windows"; $env:GOARCH="amd64"; go build -o .\dist\xavg-client-windows-amd64.exe .\dist\xavg-client.go
```

From macOS or Linux, build all common targets:

```bash
GOOS=linux GOARCH=amd64 go build -o ./dist/xavg-client-linux-amd64 ./dist/xavg-client.go
GOOS=darwin GOARCH=amd64 go build -o ./dist/xavg-client-darwin-amd64 ./dist/xavg-client.go
GOOS=darwin GOARCH=arm64 go build -o ./dist/xavg-client-darwin-arm64 ./dist/xavg-client.go
GOOS=windows GOARCH=amd64 go build -o ./dist/xavg-client-windows-amd64.exe ./dist/xavg-client.go
```

## Schedule Automatic Sync

The client includes its own scheduler. Configure the interval in `dist/.env`:

```ini
SCHEDULER=1h
```

Start the client once and keep the process running. It syncs immediately, then waits for the configured interval before syncing again.

Windows Task Scheduler can still be used to start the client when the machine boots or a user logs in:

```powershell
C:\path\to\xavg-mikrotik\dist\xavg-client.exe
```

Linux or macOS cron is no longer needed for the interval itself. If you use cron, use it only to start the long-running client after boot, for example:

```cron
@reboot cd /path/to/xavg-mikrotik && ./dist/xavg-client >> ./dist/xavg-client.log 2>&1
```

## Safety Notes

- Run `-dry-run` first
- Keep the public feed URL trusted
- Do not publish your MikroTik SSH credentials
- Keep SSH limited to trusted management networks when possible
- Put both blacklist drop rules before broad accept rules
- The client only adds entries; remove stale entries manually if your policy requires expiration
