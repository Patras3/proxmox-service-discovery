package main

import (
	"maps"
	"testing"

	"github.com/andrew-d/proxmox-service-discovery/internal/pveapi"
)

func TestQEMUInformation_NetworkKV(t *testing.T) {
	config := pveapi.QEMUConfig{
		NetworkInterfaces: map[int]string{
			0: "virtio=AA:BB:CC:DD:EE:FF,bridge=vmbr0",
		},
	}

	tests := []struct {
		name  string
		ifnum int
		want  map[string]string
	}{
		{
			name:  "basic_network",
			ifnum: 0,
			want: map[string]string{
				"virtio": "AA:BB:CC:DD:EE:FF",
				"bridge": "vmbr0",
			},
		},
		{
			name:  "nonexistent_ifnum",
			ifnum: 1,
			want:  nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			q := &qemuInformation{
				ID:     100,
				Node:   "node1",
				Config: config,
			}
			got := q.getNetworkKV(tt.ifnum)
			if !maps.Equal(got, tt.want) {
				t.Errorf("getNetworkKV() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestQEMUInformation_Bridge(t *testing.T) {
	config := pveapi.QEMUConfig{
		NetworkInterfaces: map[int]string{
			0: "virtio=AA:BB:CC:DD:EE:FF,bridge=vmbr0",
			1: "virtio=11:22:33:44:55:66",
		},
	}
	qinfo := &qemuInformation{
		ID:     100,
		Node:   "node1",
		Config: config,
	}

	if got := qinfo.getBridgeByIfnum(0); got != "vmbr0" {
		t.Errorf("getBridgeByIfnum(0) = %s, want vmbr0", got)
	}
	if got := qinfo.getBridgeByIfnum(2); got != "" {
		t.Errorf("getBridgeByIfnum(2) = %s, want empty string", got)
	}

	if got := qinfo.getBridgeByHwaddr("AA:BB:CC:DD:EE:FF"); got != "vmbr0" {
		t.Errorf("getBridgeByHwaddr(AA:BB:CC:DD:EE:FF) = %q, want vmbr0", got)
	}
	if got := qinfo.getBridgeByHwaddr("11:22:33:44:55:66"); got != "" {
		t.Errorf("getBridgeByHwaddr(11:22:33:44:55:66) = %q, want empty string", got)
	}
	if got := qinfo.getBridgeByHwaddr("00:00:00:00:00:00"); got != "" {
		t.Errorf("getBridgeByHwaddr(00:00:00:00:00:00) = %q, want empty string", got)
	}
}

func TestQEMUInformation_AllHwaddrs(t *testing.T) {
	config := pveapi.QEMUConfig{
		NetworkInterfaces: map[int]string{
			0: "virtio=AA:BB:CC:DD:EE:FF,bridge=vmbr0",
			1: "virtio=11:22:33:44:55:66,bridge=vmbr1",
		},
	}
	qinfo := &qemuInformation{
		ID:     100,
		Node:   "node1",
		Config: config,
	}

	want := map[string]bool{
		"aa:bb:cc:dd:ee:ff": true, // lower-cased
		"11:22:33:44:55:66": true,
	}
	got := qinfo.getAllHwaddrs()
	if !maps.Equal(got, want) {
		t.Errorf("allHwaddrs() = %+v, want %+v", got, want)
	}
}
