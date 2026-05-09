# xAVG Public Blacklist Client Setup

This guide shows how to use the standalone xAVG client to download the public blacklist feed and add the IPs to a MikroTik RouterOS address list named `blacklist`.

Public feed:

```text
https://xavg.bixio.xyz/api/public/blacklist.json
```

The feed returns only public IP addresses:

```json
{
  "ips": ["203.0.113.10"]
}
```

## What The Client Does

`dist/xavg-client.go`:

- Downloads the public blacklist JSON feed
- Reads the `ips` array
- Filters invalid, private, loopback, link-local, CGNAT, and multicast IPv4 addresses
- Connects to MikroTik over SSH
- Checks the existing RouterOS firewall address list
- Adds missing IPs to `/ip firewall address-list list=blacklist`

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
```

## Firewall Rule

Add a firewall rule that drops traffic from the `blacklist` address list. Put this rule near the top of the relevant chain, before broad allow rules.

For blocking inbound WAN traffic to the router:

```routeros
/ip firewall filter add chain=input src-address-list=blacklist action=drop comment="drop xAVG blacklist to router"
```

For blocking forwarded traffic through the router:

```routeros
/ip firewall filter add chain=forward src-address-list=blacklist action=drop comment="drop xAVG blacklist through router"
```

Move the rule near the top if needed:

```routeros
/ip firewall filter move [find comment="drop xAVG blacklist to router"] destination=0
/ip firewall filter move [find comment="drop xAVG blacklist through router"] destination=0
```

If your router uses interface lists, you can restrict the rule to WAN traffic:

```routeros
/ip firewall filter add chain=input in-interface-list=WAN src-address-list=blacklist action=drop comment="drop xAVG blacklist from WAN"
/ip firewall filter add chain=forward in-interface-list=WAN src-address-list=blacklist action=drop comment="drop xAVG blacklist forward from WAN"
```

## Client Configuration

Create `dist/.env` next to the executable:

```ini
XAVG_BLACKLIST_URL=https://bixio.xyz/api/public/blacklist.json

MT_HOST=192.168.88.1:22
MT_USER=xavg-client
MT_PASS=CHANGE_THIS_PASSWORD

MT_BLACKLIST_LIST=blacklist
MT_BLACKLIST_COMMENT=xavg-public-blacklist
```

Notes:

- `MT_HOST` must include the SSH port, for example `192.168.88.1:22`
- `MT_USER` and `MT_PASS` are the MikroTik SSH credentials
- `MT_BLACKLIST_LIST=blacklist` must match the firewall rules above
- `MT_BLACKLIST_COMMENT` is written on new address-list entries

You can also pass values as flags:

```powershell
.\dist\xavg-client.exe -mt-host 192.168.88.1:22 -mt-user xavg-client -mt-pass CHANGE_THIS_PASSWORD
```

## Run

Test without writing to MikroTik:

```powershell
.\dist\xavg-client.exe -dry-run
```

Run the sync:

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

Windows Task Scheduler can run:

```powershell
C:\path\to\xavg-mikrotik\dist\xavg-client.exe
```

Linux or macOS cron example, every 15 minutes:

```cron
*/15 * * * * cd /path/to/xavg-mikrotik && ./dist/xavg-client >> ./dist/xavg-client.log 2>&1
```

## Safety Notes

- Run `-dry-run` first
- Keep the public feed URL trusted
- Do not publish your MikroTik SSH credentials
- Keep SSH limited to trusted management networks when possible
- Put the blacklist drop rule before broad accept rules
- The client only adds entries; remove stale entries manually if your policy requires expiration
