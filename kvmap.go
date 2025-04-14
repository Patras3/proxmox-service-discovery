package main

import (
	"github.com/andrew-d/proxmox-service-discovery/internal/lazy"
)

// kvMap is a wrapper type that allows lazily parsing LXC or QEMU configuration
// into a map of key-value pairs.
type kvMap struct {
	inner map[int]string
	lazy  lazy.Map[int, map[string]string] // map[ifnum]KVs
}

// Get retrieves the split key-value pairs for a given network interface
// number, lazily parsing them on-demand.
//
// For example, if the LXC config has:
//
//	net0: bridge=vmbr0
//
// Then the 'inner' map will contain:
//
//	1: bridge=vmbr0
//
// And Get(0) will return:
//
//	map[string]string{
//	    "bridge": "vmbr0",
//	}
func (k *kvMap) Get(ifnum int) map[string]string {
	return k.lazy.Get(ifnum, func(int) map[string]string {
		if conf, ok := k.inner[ifnum]; ok {
			return splitKVs(conf)
		}
		return nil
	})
}
