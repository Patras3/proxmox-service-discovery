package main

import (
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/netip"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/creachadair/taskgroup"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"

	"github.com/andrew-d/proxmox-service-discovery/internal/pveapi"
	"github.com/andrew-d/proxmox-service-discovery/internal/pvelog"
)

const (
	// currentCacheVersion is the version of the cache file format.
	currentCacheVersion = 1

	// inventorySubsystem is the Prometheus subsystem for inventory metrics.
	inventorySubsystem = "inventory"
)

var (
	// State of the cluster
	nodeCountMetric = promauto.NewGauge(prometheus.GaugeOpts{
		Namespace: metricsNamespace,
		Subsystem: inventorySubsystem,
		Name:      "node_count",
		Help:      "Number of nodes in the Proxmox cluster",
	})
	lxcCountMetric = promauto.NewGauge(prometheus.GaugeOpts{
		Namespace: metricsNamespace,
		Subsystem: inventorySubsystem,
		Name:      "lxc_count",
		Help:      "Number of LXCs in the Proxmox cluster",
	})
	vmCountMetric = promauto.NewGauge(prometheus.GaugeOpts{
		Namespace: metricsNamespace,
		Subsystem: inventorySubsystem,
		Name:      "vm_count",
		Help:      "Number of VMs in the Proxmox cluster",
	})

	// State about inventory fetches
	lastInventoryUpdateMetric = promauto.NewGauge(prometheus.GaugeOpts{
		Namespace: metricsNamespace,
		Subsystem: inventorySubsystem,
		Name:      "last_inventory_update",
		Help:      "Unix timestamp of the last inventory update",
	})
	inventoryFetches = promauto.NewCounter(prometheus.CounterOpts{
		Namespace: metricsNamespace,
		Subsystem: inventorySubsystem,
		Name:      "fetches_total",
		Help:      "Total number of inventory fetches",
	})
	inventoryFetchErrors = promauto.NewCounter(prometheus.CounterOpts{
		Namespace: metricsNamespace,
		Subsystem: inventorySubsystem,
		Name:      "fetch_errors_total",
		Help:      "Total number of inventory fetch errors",
	})
)

// pveInventory is a summary of the state of the Proxmox cluster.
type pveInventory struct {
	// Version is the version of the inventory, used to determine if a
	// cached inventory is compatible with the current version of the code.
	Version int
	// CacheKey is an (opaque) key used to identify the host/cluster that
	// this inventory belongs to. This is used to ensure that the cache
	// file is not accidentally shared between different clusters.
	CacheKey string
	// NodeNames is the list of (host) node names in the cluster.
	NodeNames []string
	// NodeStats is a summary of the inventory about each node.
	NodeStats map[string]nodeInventoryStats
	// Resources is the list of resources in the cluster.
	Resources []pveInventoryItem
}

// nodeInventoryStats is a summary of the inventory about a single node.
type nodeInventoryStats struct {
	NumVMs  int
	NumLXCs int
}

// pveInventoryAddr represents information about one or more addresses in a Proxmox cluster.
type pveInventoryAddr struct {
	// Bridge is the name of the bridge the address is on.
	Bridge string
	// Addrs are the actual IP address(es).
	Addrs []netip.Addr
}

type pveInventoryItem struct {
	// Name is the name of the resource.
	Name string
	// ID is the (numeric) ID of the resource.
	ID int
	// Node is the name of the node the resource is on.
	Node string
	// Type is the type of the resource.
	Type pveItemType
	// Tags are the tags associated with the resource.
	Tags map[string]bool
	// Addrs are the IP addresses associated with the resource.
	Addrs []pveInventoryAddr
}

type pveItemType int

const (
	pveItemTypeUnknown pveItemType = iota
	pveItemTypeLXC
	pveItemTypeQEMU
)

func (t pveItemType) String() string {
	switch t {
	case pveItemTypeLXC:
		return "LXC"
	case pveItemTypeQEMU:
		return "QEMU"
	default:
		return "unknown"
	}
}

func (s *server) inventoryCacheKey() string {
	h := sha256.New()

	// Prefix with something unique to this program so we're less likely to
	// be confused with some other cache file.
	h.Write([]byte("pve-inventory-cache\n"))

	// Hash the hostname of the cluster; this does mean that if we talk to
	// only a single host in the cluster, we won't be able to use the cache
	// on other hosts, but that seems like a reasonable tradeoff.
	fmt.Fprintf(h, "%s\n", s.host)

	// Hash authentication credentials; this means that if we use a
	// different username/password we won't leak potentially sensitive
	// information.
	s.auth.WriteCacheKey(h)

	return fmt.Sprintf("%x", h.Sum(nil))
}

func (s *server) fetchInventory(ctx context.Context) (pveInventory, error) {
	// Start by fetching the list of nodes from the Proxmox API; if this
	// succeeds, then we continue.
	inventory, err := s.fetchInventoryFromProxmox(ctx)
	if err != nil {
		// If we have a cache and this is the first time we're trying to
		// fetch inventory, try loading it from the cache file.
		//
		// We only do this once to ensure that we're not loading the cache,
		// fetching an updated inventory from Proxmox, and then
		// re-loading the old and out-of-date cache again if the
		// Proxmox call fails.
		if s.cachePath != "" {
			var (
				loaded   bool
				cacheErr error
			)
			s.cacheLoadOnce.Do(func() {
				loaded = true
				inventory, cacheErr = s.loadCache()
				if cacheErr != nil {
					logger.Error("error loading inventory from cache", pvelog.Error(cacheErr))
				}
			})
			if loaded && cacheErr == nil {
				logger.Debug("loaded inventory from cache",
					slog.String("original_error", err.Error()),
				)
				return inventory, nil
			}
		}

		// If we don't have a cache, return the error.
		inventoryFetchErrors.Inc()
		return inventory, fmt.Errorf("fetching inventory from Proxmox: %w", err)
	}

	// We have a valid inventory; update metrics.
	//
	// TODO: do this in the "loaded from cache" path too?
	inventoryFetches.Inc()
	nodeCountMetric.Set(float64(len(inventory.NodeNames)))
	lxcCountMetric.Set(float64(CountSlice(inventory.Resources, func(item pveInventoryItem) bool {
		return item.Type == pveItemTypeLXC
	})))
	vmCountMetric.Set(float64(CountSlice(inventory.Resources, func(item pveInventoryItem) bool {
		return item.Type == pveItemTypeQEMU
	})))

	// On success, save the inventory to the cache.
	if s.cachePath != "" {
		if err := s.saveCache(inventory); err != nil {
			logger.Error("error saving inventory to cache", pvelog.Error(err))
			// continue; non-fatal
		}
	}

	// Update lastInventoryUpdate time now that we've fetched the
	// inventory.
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastInventoryUpdate = time.Now()
	lastInventoryUpdateMetric.Set(float64(s.lastInventoryUpdate.Unix()))
	return inventory, nil
}

func (s *server) loadCache() (inventory pveInventory, err error) {
	if s.cachePath == "" {
		return inventory, nil
	}

	// Read the cache file and unmarshal it into the inventory struct.
	var zero pveInventory
	data, err := os.ReadFile(s.cachePath)
	if err != nil {
		return zero, fmt.Errorf("reading cache file: %w", err)
	}

	if err := json.Unmarshal(data, &inventory); err != nil {
		return zero, fmt.Errorf("unmarshalling cache file: %w", err)
	}

	// Check that the cache version is compatible with the current version.
	if inventory.Version != currentCacheVersion {
		return zero, fmt.Errorf("cache version %d is incompatible with current version %d", inventory.Version, currentCacheVersion)
	}

	// Check that the cache key matches.
	if want := s.inventoryCacheKey(); inventory.CacheKey != want {
		return zero, fmt.Errorf("cache key %q does not match expected key %q", inventory.CacheKey, want)
	}

	// Update the lastInventoryUpdate time to the file's modification time
	if fileInfo, statErr := os.Stat(s.cachePath); statErr == nil {
		s.mu.Lock()
		s.lastInventoryUpdate = fileInfo.ModTime()
		s.mu.Unlock()
	}

	return inventory, nil
}

func (s *server) saveCache(inventory pveInventory) error {
	if s.cachePath == "" {
		return nil
	}

	// Update the version of the inventory to the current version.
	inventory.Version = currentCacheVersion

	// Set cache key.
	inventory.CacheKey = s.inventoryCacheKey()

	// JSON-marshal the inventory, write it to a temporary file in the same
	// directory, and then atomically rename it.
	data, err := json.Marshal(inventory)
	if err != nil {
		return fmt.Errorf("marshalling inventory to JSON: %w", err)
	}

	tmpFile, err := os.CreateTemp(filepath.Dir(s.cachePath), "pve-inventory-*.json")
	if err != nil {
		return fmt.Errorf("creating temporary file: %w", err)
	}
	defer os.Remove(tmpFile.Name())

	if _, err := tmpFile.Write(data); err != nil {
		return fmt.Errorf("writing temporary file: %w", err)
	}
	if err := tmpFile.Close(); err != nil {
		return fmt.Errorf("closing temporary file: %w", err)
	}

	if err := os.Rename(tmpFile.Name(), s.cachePath); err != nil {
		return fmt.Errorf("renaming temporary file: %w", err)
	}

	logger.Info("saved inventory to cache", slog.String("path", s.cachePath))
	return nil
}

func (s *server) fetchInventoryFromProxmox(ctx context.Context) (inventory pveInventory, _ error) {
	// Start by fetching the list of nodes from the Proxmox API
	nodes, err := s.client.GetNodes(ctx)
	if err != nil {
		return inventory, fmt.Errorf("fetching nodes: %w", err)
	}
	inventory.NodeStats = make(map[string]nodeInventoryStats, len(nodes))

	// For each node, fetch VMs and LXCs in parallel.
	var (
		g taskgroup.Group

		mu      sync.Mutex
		numLXCs int
		numVMs  int
	)
	for _, node := range nodes {
		// Save node name
		inventory.NodeNames = append(inventory.NodeNames, node.Node)

		g.Go(func() error {
			defer logger.Info("finished fetching inventory from node", "node", node.Node)
			nodeInventory, stats, err := s.fetchInventoryFromNode(ctx, node.Node)
			if err != nil {
				return err
			}

			mu.Lock()
			defer mu.Unlock()

			// Update resources
			inventory.NodeStats[node.Node] = stats
			inventory.Resources = append(inventory.Resources, nodeInventory.Resources...)

			// Update stats
			numLXCs += stats.NumLXCs
			numVMs += stats.NumVMs
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return inventory, fmt.Errorf("fetching inventory from nodes: %w", err)
	}

	logger.Debug("fetched inventory from Proxmox",
		"num_nodes", len(nodes),
		"num_vms", numVMs,
		"num_lxcs", numLXCs)

	return inventory, nil
}

func (s *server) fetchInventoryFromNode(ctx context.Context, node string) (inventory pveInventory, stats nodeInventoryStats, _ error) {
	logger := logger.With("node", node)

	// Fetch the list of VMs
	vms, err := s.client.GetQEMUVMs(ctx, node)
	if err != nil {
		return inventory, stats, fmt.Errorf("fetching VMs for node %q: %w", node, err)
	}
	stats.NumVMs = len(vms)

	// Add the VMs to the inventory
	for _, vm := range vms {
		// Skip VMs that are not running
		if vm.Status != "running" {
			continue
		}

		// Get the IP address of the VM
		addrs, err := s.fetchQEMUAddrs(ctx, node, vm.VMID)
		if err != nil {
			return inventory, stats, fmt.Errorf("fetching IP addresses for VM %q on %q: %w", vm.VMID, node, err)
		}
		logger.Debug("fetched IP addresses for VM", "vm", vm.Name, "addrs", addrs)

		inventory.Resources = append(inventory.Resources, pveInventoryItem{
			Name:  vm.Name,
			ID:    vm.VMID,
			Node:  node,
			Type:  pveItemTypeQEMU,
			Tags:  stringBoolMap(strings.Split(vm.Tags, ";")...),
			Addrs: addrs,
		})
	}

	// Fetch the list of LXCs
	lxcs, err := s.client.GetLXCs(ctx, node)
	if err != nil {
		return inventory, stats, fmt.Errorf("fetching LXCs for node %q: %w", node, err)
	}
	stats.NumLXCs = len(lxcs)

	// Add the LXCs to the inventory
	for _, lxc := range lxcs {
		// Skip LXCs that are not running
		if lxc.Status != "running" {
			continue
		}

		// Get the IP address of the VM
		addrs, err := s.fetchLXCAddrs(ctx, node, lxc.VMID)
		if err != nil {
			return inventory, stats, fmt.Errorf("fetching IP addresses for LXC %q on %q: %w", lxc.VMID, node, err)
		}
		logger.Debug("fetched IP addresses for LXC", "lxc", lxc.Name, "addrs", addrs)

		inventory.Resources = append(inventory.Resources, pveInventoryItem{
			Name:  lxc.Name,
			ID:    lxc.VMID,
			Node:  node,
			Type:  pveItemTypeLXC,
			Tags:  stringBoolMap(strings.Split(lxc.Tags, ";")...),
			Addrs: addrs,
		})
	}
	return inventory, stats, nil
}

func stringBoolMap(from ...string) map[string]bool {
	m := make(map[string]bool, len(from))
	for _, s := range from {
		m[s] = true
	}
	return m
}

func (s *server) fetchQEMUAddrs(ctx context.Context, node string, vmID int) ([]pveInventoryAddr, error) {
	logger := logger.With("vm", vmID, "node", node)

	// Start by seeing if we can find a static IP address in the QEMU config.
	conf, err := s.client.GetQEMUConfig(ctx, node, vmID)
	if err != nil {
		return nil, fmt.Errorf("fetching QEMU config for %q on %q: %w", vmID, node, err)
	}

	// Create a qemuInformation to hold the configuration and lazily calculate
	// needed information.
	qemuInfo := qemuInformation{
		ID:     vmID,
		Node:   node,
		Config: conf,
	}

	// Get all interfaces from the QEMU agent.
	interfaces, err := s.client.GetQEMUInterfaces(ctx, node, vmID)
	if err != nil {
		// If we can't get the interfaces, try to get the IP addresses from
		// the cloud-init configuration.
		if addrs := s.parseQEMUAddrsFromCloudInit(ctx, logger, &qemuInfo); len(addrs) > 0 {
			logger.Warn("no interfaces from QEMU agent; fetched IP addresses from cloud-init",
				slog.Any("addrs", addrs),
				pvelog.Error(err),
			)
			return addrs, nil
		} else {
			logger.Debug("no IP addresses found in cloud-init")
		}

		return nil, fmt.Errorf("fetching QEMU guest interfaces for %q on %q: %w", vmID, node, err)
	}
	if len(interfaces.Result) == 0 {
		// Fall back as above.
		logger.Debug("no interfaces found in QEMU guest")
		return s.parseQEMUAddrsFromCloudInit(ctx, logger, &qemuInfo), nil
	}

	logger.Debug("fetched QEMU guest interfaces", "num_interfaces", len(interfaces.Result))

	var hostConfiguredInterfaces []pveapi.AgentInterface
	if allHwaddrs := qemuInfo.getAllHwaddrs(); len(allHwaddrs) == 0 {
		// Log all interface configuration for debugging.
		var attrs []any
		for ifnum, confstr := range conf.NetworkInterfaces {
			attrs = append(attrs, slog.String(
				fmt.Sprintf("iface_net%d", ifnum),
				confstr,
			))
		}
		logger.Warn("no hardware address found for QEMU guest, returning all IP addresses", attrs...)

		// Use all interfaces if we have nothing to use as a filter.
		hostConfiguredInterfaces = interfaces.Result
	} else {
		// Filter the interfaces to just ones that are specified in the
		// QEMU configuration, by matching on the hardware address.
		for _, iface := range interfaces.Result {
			lowerHwAddr := strings.ToLower(iface.HardwareAddress)
			if !allHwaddrs[lowerHwAddr] {
				logger.Debug("skipping interface because the hardware address is unrecognized",
					slog.String("interface_name", iface.Name),
					slog.String("hardware_address", iface.HardwareAddress),
				)
				continue
			}

			// Lower-case the hardware address for consistency/so we can use it below.
			hostConfiguredInterfaces = append(hostConfiguredInterfaces, pveapi.AgentInterface{
				Name:            iface.Name,
				HardwareAddress: lowerHwAddr,
				IPAddresses:     iface.IPAddresses,
			})
		}
	}

	if len(hostConfiguredInterfaces) == 0 {
		// TODO: more log attributes
		logger.Warn("no interfaces in the guest matched the hardware addresses in the configuration")
		return nil, nil
	}

	// Now, hostConfiguredInterfaces contains all interfaces, from inside
	// the VM, that have a hardware address configured in Proxmox.
	//
	// Iterate through each of these interfaces and extract the IP
	// addresses and corresponding bridge.
	var addrs []pveInventoryAddr
	for _, iface := range hostConfiguredInterfaces {
		// Get the bridge name from the interface configuration.
		bridge := qemuInfo.getBridgeByHwaddr(iface.HardwareAddress)
		qaddrs := parseQEMUAddrs(logger, iface.IPAddresses)
		if len(qaddrs) == 0 {
			continue
		}

		addrs = append(addrs, pveInventoryAddr{
			Bridge: bridge,
			Addrs:  qaddrs,
		})
	}
	return addrs, nil
}

// parseQEMUAddrsFromCloudInit will parse the QEMU configuration for
// cloud-init information and return a list of statically-configured IP
// addresses.
//
// This is used as a fallback if we cannot fetch information from the QEMU
// agent in the guest.
func (s *server) parseQEMUAddrsFromCloudInit(
	ctx context.Context,
	logger *slog.Logger,
	qinfo *qemuInformation,
) []pveInventoryAddr {
	// The ipconfig[n] field is a comma-separated list of key-value pairs,
	// used to pass configuration to cloud-init; see the following for more
	// information:
	//    https://pve.proxmox.com/pve-docs/chapter-qm.html#qm_cloudinit
	//
	// Split and see if there's an "ip" or "ip6" key, and if so, add those
	// to our addresses list.
	var addrs []pveInventoryAddr
	for ifnum, confstr := range qinfo.Config.IPConfig {
		ipConfig := qinfo.getIPConfig(ifnum)
		if ipConfig == nil {
			// We always expect a netN for a given ipconfigN.
			logger.Warn("ipconfigN entry has no corresponding netN",
				slog.Int("ifnum", ifnum),
				slog.String("value", confstr))
			continue
		}
		bridgeName := qinfo.getBridgeByIfnum(ifnum)

		var ipAddrs []netip.Addr
		for _, key := range []string{"ip", "ip6"} {
			if ip, ok := ipConfig[key]; ok && ip != "dhcp" {
				pfx, err := netip.ParsePrefix(ip)
				if err != nil {
					logger.Warn("error parsing static address",
						slog.String("key", key),
						slog.String("address", ip),
						pvelog.Error(err))
					continue
				}
				ipAddrs = append(ipAddrs, pfx.Addr())
			}
		}

		logger.Info("parsed cloud-init configuration",
			slog.Int("ifnum", ifnum),
			slog.String("ipconfig", confstr),
			slog.Any("kv", ipConfig),
			slog.String("bridge", bridgeName),
			slog.Any("addresses", ipAddrs),
		)
		if len(ipAddrs) > 0 {
			addrs = append(addrs, pveInventoryAddr{
				Bridge: bridgeName,
				Addrs:  ipAddrs,
			})
		}
	}
	return addrs
}

// hwaddrForQEMUInterface will parse a QEMU network interface configuration and
// return the hardware address for that interface (i.e. the MAC address), or an
// empty string if no recognized hardware address is set.
func hwaddrForQEMUInterface(netConfig map[string]string) string {
	for _, key := range []string{
		"macaddr", // explicitly-configured MAC address
		"virtio",  // virtio network interfaces
		"e1000",   // E1000 network interface
		// NOTE: we can add Realtek 8139 or vmxnet3 later
		// TODO: what if we have both macaddr= and virtio=?
	} {
		if addr, ok := netConfig[key]; ok {
			return strings.ToLower(addr)
		}
	}
	return ""
}

// parseQEMUAddrs will parse the list of addresses from the QEMU agent
// and return a []netip.Addr with the result.
//
// It will skip non-IPv4/IPv6 addresses, any address that does not parse, and
// localhost addresses.
func parseQEMUAddrs(logger *slog.Logger, addrs []pveapi.AgentInterfaceAddress) []netip.Addr {
	var ret []netip.Addr
	for _, addr := range addrs {
		if addr.Type != "ipv4" && addr.Type != "ipv6" {
			continue
		}
		ip, err := netip.ParseAddr(addr.Address)
		if err != nil {
			logger.Error("parsing IP address", "address", addr.Address, pvelog.Error(err))
			continue
		}
		if ip.IsLoopback() {
			continue
		}
		ret = append(ret, ip)
	}
	return ret
}

func (s *server) fetchLXCAddrs(ctx context.Context, node string, vmID int) ([]pveInventoryAddr, error) {
	logger := logger.With("lxc", vmID, "node", node)

	// Fetch the LXC guest config (to see if we can find a static IP address).
	conf, err := s.client.GetLXCConfig(ctx, node, vmID)
	if err != nil {
		return nil, fmt.Errorf("fetching LXC config for %q on %q: %w", vmID, node, err)
	}
	logger.Debug("fetched LXC config", "config", conf)

	// Fetch all interfaces from the LXC.
	interfaces, err := s.client.GetLXCInterfaces(ctx, node, vmID)
	if err != nil {
		return nil, fmt.Errorf("fetching LXC guest interfaces for %q on %q: %w", vmID, node, err)
	}
	logger.Debug("fetched LXC guest interfaces", "num_interfaces", len(interfaces))

	return extractLXCAddrs(ctx, logger, conf, interfaces)
}

func extractLXCAddrs(
	ctx context.Context,
	logger *slog.Logger,
	config pveapi.LXCConfig,
	interfaces []pveapi.LXCInterface,
) ([]pveInventoryAddr, error) {
	ifmap := kvMap{inner: config.NetworkInterfaces}

	// Parse the provided interface configuration, building up data
	// structures that we use below.
	var (
		// hwaddrToBridge maps an interface's hardware address to the
		// bridge that it's attached to. We use it to add the Bridge
		// field to the returned pveInventoryAddr.
		hwaddrToBridge = make(map[string]string) // map["aa:bb:cc:dd:ee:ff"]"vmbr0"

		// staticAddrs contains any statically-configued addresses for
		// a given interface.
		//
		// This is used because on versions of Proxmox before 8.4, only
		// the first (possibly a random?) address is returned from the
		// "get interfaces" API call. Thus, we want to add on any
		// statically-configured addresses as well, just to be sure.
		staticAddrs = make(map[string][]netip.Addr) // map["TKTK"]ips
	)
	for ifnum := range config.NetworkInterfaces {
		kvs := ifmap.Get(ifnum)

		// We always expect a bridge name and hardware address in the
		// LXC configuration.
		//
		// If we don't have one, skip this interface.
		bridge, ok := kvs["bridge"]
		if !ok {
			logger.Warn("skipping interface because it has no bridge", slog.Any("config", kvs))
			continue
		}
		hwAddr := strings.ToLower(kvs["hwaddr"])
		if hwAddr == "" {
			logger.Warn("skipping interface with no hwaddr", slog.Any("config", kvs))
			continue
		}

		if _, ok := hwaddrToBridge[hwAddr]; ok {
			logger.Warn("duplicate hardware address found in LXC configuration",
				slog.String("interface", fmt.Sprintf("net%d", ifnum)),
				slog.String("hardware_address", hwAddr),
			)
			continue
		}
		hwaddrToBridge[hwAddr] = bridge

		// Add any statically-configured IPs to our map.
		for _, key := range []string{"ip", "ip6"} {
			if s, ok := kvs[key]; ok {
				pfx, err := netip.ParsePrefix(s)
				if err != nil {
					logger.Error("parsing static address",
						slog.String("key", key),
						slog.String("address", s),
						pvelog.Error(err))
					continue
				}

				staticAddrs[hwAddr] = append(staticAddrs[hwAddr], pfx.Addr())
			}
		}
	}

	type addrInfo struct {
		Bridge string
		Hwaddr string
		Addrs  []netip.Addr
	}

	// Now parse the interface information.
	var addrsFromLXC []addrInfo
	for _, iface := range interfaces {
		if iface.Name == "lo" {
			continue
		}

		var addrs []netip.Addr
		if iface.Inet != "" {
			pfx, err := netip.ParsePrefix(iface.Inet)
			if err == nil {
				addrs = append(addrs, pfx.Addr())
			} else {
				logger.Error("parsing IP prefix", slog.String("prefix", iface.Inet), pvelog.Error(err))
			}
		}
		if iface.Inet6 != "" {
			pfx, err := netip.ParsePrefix(iface.Inet6)
			if err == nil {
				addrs = append(addrs, pfx.Addr())
			} else {
				logger.Error("parsing IPv6 prefix", slog.String("prefix", iface.Inet6), pvelog.Error(err))
			}
		}

		if len(addrs) > 0 {
			addrsFromLXC = append(addrsFromLXC, addrInfo{
				Hwaddr: strings.ToLower(iface.HardwareAddress),
				Addrs:  addrs,

				// Bridge is unknown here; we fill it in later.
			})
		}
	}

	// If we have no hardware addresses at all, there's a fast path here:
	// just return all of the addresses from inside the LXC, since we can't
	// filter the list to just the ones that we know are configured on the
	// host. However, if we have at least one hardware address, filter our
	// list to just interfaces with a recognized hardware address, and fill
	// in the Bridge field.
	if len(hwaddrToBridge) > 0 {
		filtered := addrsFromLXC[:0]
		for _, addr := range addrsFromLXC {
			bridge, ok := hwaddrToBridge[addr.Hwaddr]
			if !ok {
				logger.Debug("skipping interface because the hardware address is unrecognized",
					//slog.String("interface_name", iface.Name),
					slog.String("hardware_address", addr.Hwaddr),
				)
				continue
			}

			addr.Bridge = bridge
			filtered = append(filtered, addr)
		}
		addrsFromLXC = filtered
	} else {
		// Log all interface configuration for debugging.
		var attrs []any
		for ifnum, confstr := range config.NetworkInterfaces {
			attrs = append(attrs, slog.String(
				fmt.Sprintf("iface_net%d", ifnum),
				confstr,
			))
		}
		logger.Warn("no hardware address found for LXC, returning all IP addresses", attrs...)
	}

	// Great, now construct our return list.
	ret := make([]pveInventoryAddr, 0, len(addrsFromLXC))
	for _, addr := range addrsFromLXC {
		// If we have any static addresses, add them as well.
		currAddrs := addr.Addrs
		if static, ok := staticAddrs[addr.Hwaddr]; ok {
			currAddrs = append(currAddrs, static...)
		}

		// Sort then deduplicate the addresses; this is a bit
		// inefficient, but we typically only have a small number of
		// addresses.
		slices.SortFunc(currAddrs, netip.Addr.Compare)
		currAddrs = slices.Compact(currAddrs)

		ret = append(ret, pveInventoryAddr{
			Bridge: addr.Bridge, // might be empty if we had no hwaddrs
			Addrs:  currAddrs,
		})
	}

	// Sort based on bridge so that we have a consistent return order.
	slices.SortFunc(ret, func(a, b pveInventoryAddr) int {
		return cmp.Compare(a.Bridge, b.Bridge)
	})
	return ret, nil
}

func splitKVs(s string) map[string]string {
	if len(s) == 0 {
		return nil
	}

	kvs := make(map[string]string)
	for _, kv := range strings.Split(s, ",") {
		key, value, ok := strings.Cut(kv, "=")
		if !ok {
			continue
		}
		kvs[key] = value
	}
	return kvs
}
