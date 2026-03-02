package v1alpha1

import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

// OverlappingRangeIPReservationSpec defines the desired state of OverlappingRangeIPReservation.
type OverlappingRangeIPReservationSpec struct {
	// ContainerID is the identifier of the container that owns this reservation.
	ContainerID string `json:"containerid,omitempty"`

	// PodRef is the namespace/name reference of the pod that owns this reservation.
	// +kubebuilder:validation:MinLength=1
	PodRef string `json:"podref"`

	// IfName is the network interface name inside the pod for this reservation.
	IfName string `json:"ifname,omitempty"`
}

// +genclient
// +kubebuilder:object:root=true
// +kubebuilder:resource:shortName=orip
// +kubebuilder:printcolumn:name="PodRef",type=string,JSONPath=`.spec.podref`,description="Namespace/name of the owning pod"
// +kubebuilder:printcolumn:name="IfName",type=string,JSONPath=`.spec.ifname`,description="Network interface name"
// +kubebuilder:printcolumn:name="ContainerID",type=string,JSONPath=`.spec.containerid`,priority=1,description="Container identifier"

// OverlappingRangeIPReservation is the Schema for the overlappingrangeipreservations API.
type OverlappingRangeIPReservation struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec OverlappingRangeIPReservationSpec `json:"spec"`
}

// +kubebuilder:object:root=true

// OverlappingRangeIPReservationList contains a list of OverlappingRangeIPReservation.
type OverlappingRangeIPReservationList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`

	Items []OverlappingRangeIPReservation `json:"items"`
}
