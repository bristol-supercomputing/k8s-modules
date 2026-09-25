package v1alpha1

import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

// ModuleNodeReport records what the module NRI plugin discovered on one
// node. It is named after the node and owned by it.
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster,shortName=mnr
// +kubebuilder:printcolumn:name="Discovered",type=date,JSONPath=`.status.discoveredAt`
type ModuleNodeReport struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Status ModuleNodeReportStatus `json:"status,omitempty"`
}

type ModuleNodeReportStatus struct {
	// +optional
	DiscoveredAt *metav1.Time `json:"discoveredAt,omitempty"`
	// +optional
	// +listType=map
	// +listMapKey=module
	Modules []HostModuleReport `json:"modules,omitempty"`
}

// HostModuleReport is the discovered closure of one host Module.
type HostModuleReport struct {
	Module string `json:"module"`
	Family string `json:"family"`
	Ready  bool   `json:"ready"`
	// +optional
	ClosureHash string `json:"closureHash,omitempty"`
	// +optional
	GlibcRequired string `json:"glibcRequired,omitempty"`
	// +optional
	Lib []LibraryFile `json:"lib,omitempty"`
	// +optional
	Deps []LibraryFile `json:"deps,omitempty"`
	// +optional
	Missing []string `json:"missing,omitempty"`
	// +optional
	Message string `json:"message,omitempty"`
}

type LibraryFile struct {
	Soname string `json:"soname"`
	Path   string `json:"path"`
}

// +kubebuilder:object:root=true
type ModuleNodeReportList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ModuleNodeReport `json:"items"`
}

func init() {
	SchemeBuilder.Register(&ModuleNodeReport{}, &ModuleNodeReportList{})
}
