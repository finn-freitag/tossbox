package model

// NodeCapabilities is the JSON form of the agent's Capabilities message
// (stored in nodes.capabilities and returned by the API).
type NodeCapabilities struct {
	Arch          string            `json:"arch"`
	OS            string            `json:"os"`
	HostCores     int               `json:"host_cores"`
	HostRAMMB     int64             `json:"host_ram_mb"`
	HostStorageGB int64             `json:"host_storage_gb"`
	Limits        NodeLimits        `json:"limits"`
	Drivers       []DriverInfo      `json:"drivers"`
	LicensePools  []LicensePoolInfo `json:"license_pools"`
	// LocalImages are the node's images for local:<name> templates.
	LocalImages []string `json:"local_images"`
}

// NodeLimits from the node config.
type NodeLimits struct {
	RAMMB      int64 `json:"ram_mb"`
	CPUPercent int   `json:"cpu_percent"`
	StorageGB  int64 `json:"storage_gb"`
}

// DriverInfo describes one enabled driver on a node.
type DriverInfo struct {
	Name            string           `json:"name"`
	Families        []string         `json:"families"`
	Services        []string         `json:"services"`
	Backends        []string         `json:"backends"`
	FixedSlots      int              `json:"fixed_slots"`
	Healthy         bool             `json:"healthy"`
	Message         string           `json:"message"`
	PhysicalDevices []PhysicalDevice `json:"physical_devices"`
	NetworkPolicies []string         `json:"network_policies"`
	GPUMode         string           `json:"gpu_mode"`  // "", virgl, sriov, render
	GPUSlots        int              `json:"gpu_slots"` // -1 = shared (unlimited)
	GPUFamilies     []string         `json:"gpu_families"`
}

// PhysicalDevice is a phone attached to a usb-phone node.
type PhysicalDevice struct {
	Serial    string `json:"serial"`
	Family    string `json:"family"`
	Model     string `json:"model"`
	Available bool   `json:"available"`
}

// LicensePoolInfo describes a license pool on a node (keys stay on the node).
type LicensePoolInfo struct {
	Name  string `json:"name"`
	Type  string `json:"type"`
	Total int    `json:"total"`
	InUse int    `json:"in_use"`
}

// NodeUsage is the JSON form of the agent's ResourceUsage message.
type NodeUsage struct {
	RAMUsedMB      int64   `json:"ram_used_mb"`
	CPUUsedPercent float64 `json:"cpu_used_percent"`
	StorageUsedGB  int64   `json:"storage_used_gb"`
	// Whole host (not only Tossbox devices).
	HostRAMUsedMB      int64 `json:"host_ram_used_mb"`
	HostStorageUsedGB  int64 `json:"host_storage_used_gb"`
	HostStorageTotalGB int64 `json:"host_storage_total_gb"`
}
