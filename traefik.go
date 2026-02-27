package main

import (
	"net/netip"
	"net/url"
	"strings"
)

// traefikOverrides holds DNS name aliases and IP overrides extracted from
// traefik labels in Proxmox VM/CT descriptions.
type traefikOverrides struct {
	// Aliases are additional DNS names for this resource, extracted from
	// traefik.http.routers.<name>.rule=Host(`<name>.zone`) labels.
	Aliases []string
	// OverrideAddr is an IP address override from
	// traefik.http.services.<name>.loadbalancer.server.url=http://IP:port
	OverrideAddr *netip.Addr
}

// parseTraefikLabels parses traefik labels from a Proxmox description field
// and extracts DNS aliases and IP overrides.
func parseTraefikLabels(description string, dnsZone string) traefikOverrides {
	var result traefikOverrides
	if description == "" {
		return result
	}

	// Proxmox sometimes URL-encodes descriptions
	decoded, err := url.QueryUnescape(description)
	if err == nil {
		description = decoded
	}

	labels := make(map[string]string)
	for _, line := range strings.Split(description, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "traefik.") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) == 2 {
			labels[strings.ToLower(strings.TrimSpace(parts[0]))] = strings.TrimSpace(parts[1])
		}
	}

	if enabled, ok := labels["traefik.enable"]; !ok || enabled != "true" {
		return result
	}

	// Extract aliases from router rules: Host(`name.zone`)
	zoneSuffix := "." + dnsZone
	seen := make(map[string]bool)
	for key, val := range labels {
		if strings.Contains(key, ".routers.") && strings.HasSuffix(key, ".rule") {
			// Extract hostname from Host(`...`)
			hosts := extractHostsFromRule(val)
			for _, host := range hosts {
				// Strip the zone suffix to get just the name
				if strings.HasSuffix(host, zoneSuffix) {
					name := strings.TrimSuffix(host, zoneSuffix)
					// Also strip .int.dadas.eu etc - we want just the service name
					// Check common zone patterns
					for _, z := range []string{".int.dadas.eu", ".ip.dadas.eu"} {
						if strings.HasSuffix(host, z) {
							name = strings.TrimSuffix(host, z)
							break
						}
					}
					if name != "" && !seen[name] {
						result.Aliases = append(result.Aliases, name)
						seen[name] = true
					}
				} else {
					// Try stripping known zone patterns directly
					for _, z := range []string{".int.dadas.eu", ".ip.dadas.eu"} {
						if strings.HasSuffix(host, z) {
							name := strings.TrimSuffix(host, z)
							if name != "" && !seen[name] {
								result.Aliases = append(result.Aliases, name)
								seen[name] = true
							}
						}
					}
				}
			}
		}
	}

	// Extract override IP from server.url labels
	for key, val := range labels {
		if strings.Contains(key, ".loadbalancer.server.url") {
			parsed, err := url.Parse(val)
			if err != nil {
				continue
			}
			host := parsed.Hostname()
			if addr, err := netip.ParseAddr(host); err == nil {
				result.OverrideAddr = &addr
				break
			}
		}
	}

	return result
}

// extractHostsFromRule extracts hostnames from a Traefik rule like
// Host(`foo.example.com`) or Host(`a.example.com`,`b.example.com`)
func extractHostsFromRule(rule string) []string {
	var hosts []string
	// Find Host(...) patterns
	for {
		idx := strings.Index(rule, "Host(")
		if idx == -1 {
			break
		}
		rule = rule[idx+5:]
		end := strings.Index(rule, ")")
		if end == -1 {
			break
		}
		inner := rule[:end]
		rule = rule[end+1:]

		// Parse comma-separated backtick-quoted hostnames
		for _, part := range strings.Split(inner, ",") {
			part = strings.TrimSpace(part)
			part = strings.Trim(part, "`\"' ")
			if part != "" {
				hosts = append(hosts, part)
			}
		}
	}
	return hosts
}
