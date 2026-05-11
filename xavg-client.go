package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
)

const (
	defaultIPFeedURL   = "https://xavg.bixio.xyz/api/public/blacklist.json"
	defaultCIDRFeedURL = "https://xavg.bixio.xyz/api/public/cidr-blacklist.json"
)

type Config struct {
	IPFeedURL   string
	CIDRFeedURL string
	MTHost      string
	MTUser      string
	MTPass      string
	IPList      string
	CIDRList    string
	IPComment   string
	CIDRComment string
	DryRun      bool
}

type BlacklistFeed struct {
	IPs   []string `json:"ips"`
	CIDRs []string `json:"cidrs"`
}

func main() {
	_ = loadEnvFile(".env")

	cfg := Config{}
	flag.StringVar(&cfg.IPFeedURL, "url-ip", "", "public xAVG IPv4 blacklist JSON URL; defaults to XAVG_BLACKLIST_URL or https://xavg.bixio.xyz/api/public/blacklist.json")
	flag.StringVar(&cfg.CIDRFeedURL, "url-cidr", "", "public xAVG CIDR blacklist JSON URL; defaults to XAVG_CIDR_BLACKLIST_URL or https://xavg.bixio.xyz/api/public/cidr-blacklist.json")
	flag.StringVar(&cfg.MTHost, "mt-host", "", "MikroTik SSH endpoint; defaults to MT_HOST or 192.168.88.1:22")
	flag.StringVar(&cfg.MTUser, "mt-user", "", "MikroTik SSH username; defaults to MT_USER or xavg-client")
	flag.StringVar(&cfg.MTPass, "mt-pass", "", "MikroTik SSH password; defaults to MT_PASS")
	flag.StringVar(&cfg.IPList, "list-ip", "", "MikroTik IPv4 address-list name; defaults to MT_BLACKLIST_LIST or blacklist")
	flag.StringVar(&cfg.CIDRList, "list-cidr", "", "MikroTik CIDR address-list name; defaults to MT_CIDR_BLACKLIST_LIST or cidr_blacklist")
	flag.StringVar(&cfg.IPComment, "comment-ip", "", "comment for IPv4 address-list entries; defaults to MT_BLACKLIST_COMMENT or xavg-public-blacklist")
	flag.StringVar(&cfg.CIDRComment, "comment-cidr", "", "comment for CIDR address-list entries; defaults to MT_CIDR_BLACKLIST_COMMENT or xavg-public-cidr-blacklist")
	flag.BoolVar(&cfg.DryRun, "dry-run", false, "print changes without writing to MikroTik")
	flag.Parse()

	cfg.IPFeedURL = firstNonEmpty(cfg.IPFeedURL, getenv("XAVG_BLACKLIST_URL", ""), defaultIPFeedURL)
	cfg.CIDRFeedURL = firstNonEmpty(cfg.CIDRFeedURL, getenv("XAVG_CIDR_BLACKLIST_URL", ""), defaultCIDRFeedURL)
	cfg.MTHost = firstNonEmpty(cfg.MTHost, getenv("MT_HOST", ""), "192.168.88.1:22")
	cfg.MTUser = firstNonEmpty(cfg.MTUser, getenv("MT_USER", ""), "xavg-client")
	cfg.MTPass = firstNonEmpty(cfg.MTPass, getenv("MT_PASS", ""))
	cfg.IPList = firstNonEmpty(cfg.IPList, getenv("MT_BLACKLIST_LIST", ""), "blacklist")
	cfg.CIDRList = firstNonEmpty(cfg.CIDRList, getenv("MT_CIDR_BLACKLIST_LIST", ""), "cidr_blacklist")
	cfg.IPComment = firstNonEmpty(cfg.IPComment, getenv("MT_BLACKLIST_COMMENT", ""), "xavg-public-blacklist")
	cfg.CIDRComment = firstNonEmpty(cfg.CIDRComment, getenv("MT_CIDR_BLACKLIST_COMMENT", ""), "xavg-public-cidr-blacklist")

	if cfg.MTPass == "" {
		log.Fatal("MT_PASS is required; set it in .env, environment, or -mt-pass")
	}

	interval, err := time.ParseDuration(getenv("SCHEDULER", "1h"))
	if err != nil {
		log.Fatal("invalid SCHEDULER duration:", err)
	}

	log.Printf("scheduler started: running sync now and every %s", interval)
	for {
		if err := syncAllFeeds(cfg); err != nil {
			log.Printf("sync failed: %v", err)
		}

		timer := time.NewTimer(interval)
		<-timer.C
	}
}

func syncAllFeeds(cfg Config) error {
	if err := syncFeedToList(cfg, cfg.IPFeedURL, cfg.IPList, cfg.IPComment, "ip"); err != nil {
		return err
	}

	if err := syncFeedToList(cfg, cfg.CIDRFeedURL, cfg.CIDRList, cfg.CIDRComment, "cidr"); err != nil {
		return err
	}

	return nil
}

func syncFeedToList(cfg Config, feedURL, list, comment, mode string) error {
	entries, err := fetchBlacklist(feedURL, mode)
	if err != nil {
		return fmt.Errorf("fetch %s blacklist: %w", mode, err)
	}
	log.Printf("loaded %d %s entries from %s", len(entries), mode, feedURL)

	client, err := newSSHClient(cfg)
	if err != nil {
		return fmt.Errorf("mikrotik ssh: %w", err)
	}
	defer client.Close()

	existing, err := existingAddressList(client, list)
	if err != nil {
		return fmt.Errorf("load existing MikroTik address-list: %w", err)
	}
	log.Printf("loaded %d existing entries from MikroTik address-list %q", len(existing), list)

	added := 0
	skipped := 0
	for _, entry := range entries {
		if existing[entry] {
			skipped++
			continue
		}

		if cfg.DryRun {
			log.Printf("[dry-run] would add %s to address-list %q", entry, list)
			added++
			continue
		}

		if err := addAddressListEntry(client, list, entry, comment); err != nil {
			log.Printf("add %s failed: %v", entry, err)
			continue
		}
		existing[entry] = true
		added++
		log.Printf("added %s to address-list %q", entry, list)
	}

	if cfg.DryRun {
		log.Printf("dry-run complete for %q: would add=%d skipped_existing=%d", list, added, skipped)
		return nil
	}
	log.Printf("sync complete for %q: added=%d skipped_existing=%d", list, added, skipped)
	return nil
}

func fetchBlacklist(url, mode string) ([]string, error) {
	client := &http.Client{Timeout: 30 * time.Second}
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var feed BlacklistFeed
	if err := json.NewDecoder(resp.Body).Decode(&feed); err != nil {
		return nil, err
	}

	seen := map[string]bool{}

	if mode == "ip" {
		for _, raw := range feed.IPs {
			entry, ok := normalizePublicIPv4OrCIDR(raw)
			if !ok || strings.Contains(entry, "/") {
				continue
			}
			seen[entry] = true
		}
	} else {
		for _, raw := range feed.CIDRs {
			entry, ok := normalizePublicIPv4OrCIDR(raw)
			if !ok || !strings.Contains(entry, "/") {
				continue
			}
			seen[entry] = true
		}
	}

	entries := make([]string, 0, len(seen))
	for entry := range seen {
		entries = append(entries, entry)
	}
	sort.Slice(entries, func(i, j int) bool {
		iStart, iPrefix := addressSortKey(entries[i])
		jStart, jPrefix := addressSortKey(entries[j])
		if iStart == jStart {
			return iPrefix < jPrefix
		}
		return iStart < jStart
	})

	return entries, nil
}

func newSSHClient(cfg Config) (*ssh.Client, error) {
	sshConfig := &ssh.ClientConfig{
		User: cfg.MTUser,
		Auth: []ssh.AuthMethod{
			ssh.Password(cfg.MTPass),
		},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         15 * time.Second,
	}
	return ssh.Dial("tcp", cfg.MTHost, sshConfig)
}

func existingAddressList(client *ssh.Client, list string) (map[string]bool, error) {
	out, err := sshRun(client, fmt.Sprintf(`/ip firewall address-list print terse where list="%s"`, escapeMT(list)))
	if err != nil {
		return nil, err
	}

	existing := map[string]bool{}
	for _, line := range strings.Split(out, "\n") {
		for _, field := range strings.Fields(line) {
			if strings.HasPrefix(field, "address=") {
				value := strings.TrimPrefix(field, "address=")
				entry, ok := normalizePublicIPv4OrCIDR(value)
				if ok {
					existing[entry] = true
				}
			}
		}
	}
	return existing, nil
}

func addAddressListEntry(client *ssh.Client, list, ip, comment string) error {
	cmd := fmt.Sprintf(
		`/ip firewall address-list add list="%s" address=%s comment="%s"`,
		escapeMT(list),
		escapeMT(ip),
		escapeMT(comment),
	)
	_, err := sshRun(client, cmd)
	return err
}

func sshRun(client *ssh.Client, cmd string) (string, error) {
	session, err := client.NewSession()
	if err != nil {
		return "", err
	}
	defer session.Close()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	session.Stdout = &stdout
	session.Stderr = &stderr

	if err := session.Run(cmd); err != nil {
		out := strings.TrimSpace(stdout.String())
		errOut := strings.TrimSpace(stderr.String())
		combined := strings.TrimSpace(fmt.Sprintf("%s %s", out, errOut))
		if combined == "" {
			combined = "no output"
		}
		return out, fmt.Errorf("%w: %s", err, combined)
	}

	return stdout.String(), nil
}

func isPublicIPv4(value string) bool {
	ip := net.ParseIP(value)
	if ip == nil || ip.To4() == nil {
		return false
	}

	privateRanges := []string{
		"0.0.0.0/8",
		"10.0.0.0/8",
		"100.64.0.0/10",
		"127.0.0.0/8",
		"169.254.0.0/16",
		"172.16.0.0/12",
		"192.168.0.0/16",
		"224.0.0.0/4",
	}
	for _, cidr := range privateRanges {
		_, network, _ := net.ParseCIDR(cidr)
		if network.Contains(ip) {
			return false
		}
	}
	return true
}

func normalizePublicIPv4OrCIDR(value string) (string, bool) {
	v := strings.TrimSpace(value)
	if v == "" {
		return "", false
	}

	if strings.Contains(v, "/") {
		_, network, err := net.ParseCIDR(v)
		if err != nil || network == nil || network.IP.To4() == nil {
			return "", false
		}

		base := network.IP.Mask(network.Mask)
		if !isPublicIPv4(base.String()) {
			return "", false
		}

		ones, _ := network.Mask.Size()
		return fmt.Sprintf("%s/%d", base.String(), ones), true
	}

	ip := net.ParseIP(v)
	if ip == nil || ip.To4() == nil || !isPublicIPv4(v) {
		return "", false
	}

	return ip.To4().String(), true
}

func addressSortKey(value string) (uint32, int) {
	v := strings.TrimSpace(value)
	if strings.Contains(v, "/") {
		_, network, err := net.ParseCIDR(v)
		if err == nil && network != nil && network.IP.To4() != nil {
			ones, _ := network.Mask.Size()
			return ipToUint32(network.IP.Mask(network.Mask).String()), ones
		}
	}

	return ipToUint32(v), 32
}

func ipToUint32(value string) uint32 {
	ip := net.ParseIP(value).To4()
	if ip == nil {
		return 0
	}
	return uint32(ip[0])<<24 | uint32(ip[1])<<16 | uint32(ip[2])<<8 | uint32(ip[3])
}

func escapeMT(s string) string {
	return strings.ReplaceAll(s, `"`, `'`)
}

func loadEnvFile(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}

	for _, raw := range strings.Split(string(data), "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		i := strings.Index(line, "=")
		if i < 0 {
			continue
		}

		key := strings.TrimSpace(line[:i])
		value := strings.TrimSpace(line[i+1:])
		value = strings.Trim(value, `"`)
		value = strings.Trim(value, `'`)

		if os.Getenv(key) == "" {
			_ = os.Setenv(key, value)
		}
	}

	return nil
}

func getenv(key, fallback string) string {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	return value
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
