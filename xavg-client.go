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

const defaultFeedURL = "https://bixio.xyz/api/public/blacklist.json"

type Config struct {
	FeedURL string
	MTHost  string
	MTUser  string
	MTPass  string
	List    string
	Comment string
	DryRun  bool
}

type BlacklistFeed struct {
	IPs []string `json:"ips"`
}

func main() {
	_ = loadEnvFile(".env")

	cfg := Config{}
	flag.StringVar(&cfg.FeedURL, "url", "", "public xAVG blacklist JSON URL; defaults to XAVG_BLACKLIST_URL or https://bixio.xyz/api/public/blacklist.json")
	flag.StringVar(&cfg.MTHost, "mt-host", "", "MikroTik SSH endpoint; defaults to MT_HOST or 192.168.88.1:22")
	flag.StringVar(&cfg.MTUser, "mt-user", "", "MikroTik SSH username; defaults to MT_USER or xavg-client")
	flag.StringVar(&cfg.MTPass, "mt-pass", "", "MikroTik SSH password; defaults to MT_PASS")
	flag.StringVar(&cfg.List, "list", "", "MikroTik address-list name; defaults to MT_BLACKLIST_LIST or blacklist")
	flag.StringVar(&cfg.Comment, "comment", "", "comment for new address-list entries; defaults to MT_BLACKLIST_COMMENT or xavg-public-blacklist")
	flag.BoolVar(&cfg.DryRun, "dry-run", false, "print changes without writing to MikroTik")
	flag.Parse()

	cfg.FeedURL = firstNonEmpty(cfg.FeedURL, getenv("XAVG_BLACKLIST_URL", ""), defaultFeedURL)
	cfg.MTHost = firstNonEmpty(cfg.MTHost, getenv("MT_HOST", ""), "192.168.88.1:22")
	cfg.MTUser = firstNonEmpty(cfg.MTUser, getenv("MT_USER", ""), "xavg-client")
	cfg.MTPass = firstNonEmpty(cfg.MTPass, getenv("MT_PASS", ""))
	cfg.List = firstNonEmpty(cfg.List, getenv("MT_BLACKLIST_LIST", ""), "blacklist")
	cfg.Comment = firstNonEmpty(cfg.Comment, getenv("MT_BLACKLIST_COMMENT", ""), "xavg-public-blacklist")

	if cfg.MTPass == "" {
		log.Fatal("MT_PASS is required; set it in .env, environment, or -mt-pass")
	}

	ips, err := fetchBlacklist(cfg.FeedURL)
	if err != nil {
		log.Fatal("fetch blacklist:", err)
	}
	log.Printf("loaded %d public IPs from %s", len(ips), cfg.FeedURL)

	client, err := newSSHClient(cfg)
	if err != nil {
		log.Fatal("mikrotik ssh:", err)
	}
	defer client.Close()

	existing, err := existingAddressList(client, cfg.List)
	if err != nil {
		log.Fatal("load existing MikroTik address-list:", err)
	}
	log.Printf("loaded %d existing entries from MikroTik address-list %q", len(existing), cfg.List)

	added := 0
	skipped := 0
	for _, ip := range ips {
		if existing[ip] {
			skipped++
			continue
		}

		if cfg.DryRun {
			log.Printf("[dry-run] would add %s to address-list %q", ip, cfg.List)
			added++
			continue
		}

		if err := addAddressListEntry(client, cfg.List, ip, cfg.Comment); err != nil {
			log.Printf("add %s failed: %v", ip, err)
			continue
		}
		existing[ip] = true
		added++
		log.Printf("added %s to address-list %q", ip, cfg.List)
	}

	if cfg.DryRun {
		log.Printf("dry-run complete: would add=%d skipped_existing=%d", added, skipped)
		return
	}
	log.Printf("sync complete: added=%d skipped_existing=%d", added, skipped)
}

func fetchBlacklist(url string) ([]string, error) {
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Get(url)
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
	for _, raw := range feed.IPs {
		ip := strings.TrimSpace(raw)
		if !isPublicIPv4(ip) {
			continue
		}
		seen[ip] = true
	}

	ips := make([]string, 0, len(seen))
	for ip := range seen {
		ips = append(ips, ip)
	}
	sort.Slice(ips, func(i, j int) bool {
		return ipToUint32(ips[i]) < ipToUint32(ips[j])
	})

	return ips, nil
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
				ip := strings.TrimPrefix(field, "address=")
				if isPublicIPv4(ip) {
					existing[ip] = true
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
