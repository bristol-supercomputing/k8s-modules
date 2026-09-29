package v1alpha1

import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

const (
	LabelFamily  = "modules.isambard.ac.uk/family"
	LabelDefault = "modules.isambard.ac.uk/default"

	AnnotationLoad           = "modules.isambard.ac.uk/load"
	AnnotationResolvedPrefix = "resolved.modules.isambard.ac.uk/"
	AnnotationEnvPrefix      = "env.modules.isambard.ac.uk/"

	MountRoot        = "/opt/modules"
	EnvLDLibraryPath = "LD_LIBRARY_PATH"

	AnnotationRequiredPlugins = "required-plugins.noderesource.dev/pod"
	RequiredPluginsValue      = `["modules"]`
	NRIPluginName             = "modules"
	LabelHostPrefix           = "modules.isambard.ac.uk/host-"

	AnnotationLoadContainers = "modules.isambard.ac.uk/load-containers"

	// DRA: every Module is a shareable device of this driver, requested as
	// ResourcePrefix+<module> or FamilyResourcePrefix+<family>.
	DriverName           = "modules.isambard.ac.uk"
	ResourcePrefix       = "modules.isambard.ac.uk/"
	FamilyResourcePrefix = "family.modules.isambard.ac.uk/"
	DeviceClassSuffix    = ".modules.isambard.ac.uk"
	FamilyClassSuffix    = ".family.modules.isambard.ac.uk"
	LabelManaged         = "modules.isambard.ac.uk/managed"
	AttrModule           = "module"
	AttrFamily           = "family"
	AttrDefault          = "default"

	// EnvMarkerPrefix starts the env var the DRA driver's CDI edits set for
	// each module a container gets; its value is the module's resolved
	// annotation value. EnvModules lists them for the workload.
	EnvMarkerPrefix = "BRICS_MODULE_"
	EnvModules      = "BRICS_MODULES"

	ConditionResolved     = "Resolved"
	ConditionConflictFree = "ConflictFree"
)

// Module is a versioned bundle of shared libraries that pods can load.
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster
// +kubebuilder:printcolumn:name="Family",type=string,JSONPath=`.spec.family`
// +kubebuilder:printcolumn:name="Version",type=string,JSONPath=`.spec.version`
// +kubebuilder:printcolumn:name="Resolved",type=string,JSONPath=`.status.conditions[?(@.type=="Resolved")].status`
// +kubebuilder:printcolumn:name="Reason",type=string,JSONPath=`.status.conditions[?(@.type=="Resolved")].reason`
// +kubebuilder:validation:XValidation:rule="self.metadata.name == self.spec.family + '-' + self.spec.version",message="metadata.name must be <spec.family>-<spec.version>"
type Module struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ModuleSpec   `json:"spec"`
	Status ModuleStatus `json:"status,omitempty"`
}

// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="spec is immutable"
// +kubebuilder:validation:XValidation:rule="!has(self.dependsOn) || !((self.family + '-' + self.version) in self.dependsOn)",message="a module cannot depend on itself"
type ModuleSpec struct {
	// +kubebuilder:validation:MaxLength=40
	// +kubebuilder:validation:Pattern=`^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`
	Family string `json:"family"`
	// +kubebuilder:validation:MaxLength=20
	// +kubebuilder:validation:Pattern=`^[A-Za-z0-9]([A-Za-z0-9._-]*[A-Za-z0-9])?$`
	Version string `json:"version"`

	Source ModuleSource `json:"source"`

	// DependsOn lists exact Module names.
	// +optional
	// +listType=set
	// +kubebuilder:validation:MaxItems=32
	// +kubebuilder:validation:items:MaxLength=63
	// +kubebuilder:validation:items:Pattern=`^[a-z0-9]([a-z0-9.-]*[a-z0-9])?$`
	DependsOn []string `json:"dependsOn,omitempty"`

	// +optional
	Env *ModuleEnv `json:"env,omitempty"`

	// MaskPaths are absolute container paths, applied in phase 3.
	// +optional
	// +kubebuilder:validation:MaxItems=32
	// +kubebuilder:validation:items:MaxLength=256
	// +kubebuilder:validation:items:Pattern=`^/[A-Za-z0-9._/+-]*$`
	MaskPaths []string `json:"maskPaths,omitempty"`

	// +optional
	Access *ModuleAccess `json:"access,omitempty"`
}

// +kubebuilder:validation:XValidation:rule="[has(self.image), has(self.host), has(self.pvc)].filter(x, x).size() == 1",message="exactly one of image, host or pvc must be set"
type ModuleSource struct {
	// +optional
	Image *ImageSource `json:"image,omitempty"`
	// +optional
	Host *HostSource `json:"host,omitempty"`
	// +optional
	PVC *PVCSource `json:"pvc,omitempty"`
}

type ImageSource struct {
	// +kubebuilder:validation:MaxLength=512
	// +kubebuilder:validation:Pattern=`^[^;:\s]+(:[0-9]+)?(/[^;:\s]+)*@sha256:[a-f0-9]{64}$`
	Reference string `json:"reference"`
}

type HostSource struct {
	Discover HostDiscover `json:"discover"`
	// PreferHost lists dependency sonames placed in lib/ (prepended to
	// LD_LIBRARY_PATH) instead of deps/, for when the image's copy is known
	// to be incompatible.
	// +optional
	// +kubebuilder:validation:MaxItems=16
	// +kubebuilder:validation:items:MaxLength=128
	// +kubebuilder:validation:items:Pattern=`^lib[A-Za-z0-9_.+-]+\.so(\.[0-9]+)*$`
	PreferHost []string `json:"preferHost,omitempty"`
}

type HostDiscover struct {
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=32
	// +kubebuilder:validation:items:MaxLength=128
	// +kubebuilder:validation:items:Pattern=`^lib[A-Za-z0-9_.+-]+\.so(\.[0-9]+)*$`
	Roots []string `json:"roots"`
}

type PVCSource struct {
	// +kubebuilder:validation:MaxLength=253
	// +kubebuilder:validation:Pattern=`^[a-z0-9]([-a-z0-9]*[a-z0-9])?(\.[a-z0-9]([-a-z0-9]*[a-z0-9])?)*$`
	ClaimName string `json:"claimName"`
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=256
	// +kubebuilder:validation:Pattern=`^[A-Za-z0-9._+-][A-Za-z0-9._/+-]*$`
	// +kubebuilder:validation:XValidation:rule="!self.split('/').exists(s, s == '..')",message="subPath must not contain '..' segments"
	SubPath string `json:"subPath"`
}

type ModuleEnv struct {
	// PrependPath maps LD_LIBRARY_PATH to paths relative to the mount path.
	// +optional
	// +kubebuilder:validation:MaxProperties=1
	// +kubebuilder:validation:XValidation:rule="self.all(k, k == 'LD_LIBRARY_PATH')",message="only LD_LIBRARY_PATH may be prepended"
	// +kubebuilder:validation:XValidation:rule="self.all(k, self[k].all(p, !p.split('/').exists(s, s == '..')))",message="prependPath entries must not contain '..' segments"
	PrependPath map[string]PathList `json:"prependPath,omitempty"`

	// +optional
	// +kubebuilder:validation:MaxProperties=32
	// +kubebuilder:validation:XValidation:rule="self.all(k, k.size() <= 63 && k.matches('^[A-Za-z][A-Za-z0-9_]*[A-Za-z0-9]$'))",message="env.set names must match ^[A-Za-z][A-Za-z0-9_]*[A-Za-z0-9]$ and be at most 63 characters"
	// +kubebuilder:validation:XValidation:rule="self.all(k, !(k in ['LD_PRELOAD', 'LD_LIBRARY_PATH', 'LD_AUDIT', 'PATH']))",message="env.set may not set LD_PRELOAD, LD_LIBRARY_PATH, LD_AUDIT or PATH"
	Set map[string]string `json:"set,omitempty"`
}

// PathList is a list of relative paths.
// +kubebuilder:validation:MaxItems=16
// +kubebuilder:validation:items:MaxLength=256
// +kubebuilder:validation:items:Pattern=`^[A-Za-z0-9._+-][A-Za-z0-9._/+-]*$`
type PathList []string

type ModuleAccess struct {
	// MatchLabels are namespace labels; empty means every namespace.
	// +optional
	// +kubebuilder:validation:MaxProperties=16
	MatchLabels map[string]string `json:"matchLabels,omitempty"`
}

type ModuleStatus struct {
	// +optional
	Resolved *Resolved `json:"resolved,omitempty"`
	// +optional
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// Resolved is the flattened closure of a Module.
type Resolved struct {
	Modules []string `json:"modules"`
	// +optional
	Volumes []ResolvedVolume `json:"volumes,omitempty"`
	// +optional
	Mounts []ResolvedMount `json:"mounts,omitempty"`
	// +optional
	Env         []ResolvedEnvVar  `json:"env,omitempty"`
	Annotations map[string]string `json:"annotations"`
}

type ResolvedVolume struct {
	Name      string `json:"name"`
	Reference string `json:"reference"`
}

type ResolvedMount struct {
	Name      string `json:"name"`
	MountPath string `json:"mountPath"`
}

type ResolvedEnvVar struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// +kubebuilder:object:root=true
type ModuleList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Module `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Module{}, &ModuleList{})
}
