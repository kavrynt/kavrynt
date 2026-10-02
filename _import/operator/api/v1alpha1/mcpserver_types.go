package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

const (
	MCPServerKind = "MCPServer"

	ConditionRegistered = "Registered"
)

type MCPServerSpec struct {
	Version     string            `json:"version"`
	Transport   string            `json:"transport"`
	Command     string            `json:"command,omitempty"`
	Args        []string          `json:"args,omitempty"`
	Endpoint    string            `json:"endpoint,omitempty"`
	Environment map[string]string `json:"environment,omitempty"`
}

type MCPServerStatus struct {
	ObservedGeneration int64              `json:"observedGeneration,omitempty"`
	RegistrySyncedAt   *metav1.Time       `json:"registrySyncedAt,omitempty"`
	RegistryError      string             `json:"registryError,omitempty"`
	Conditions         []metav1.Condition `json:"conditions,omitempty"`
}

type MCPServer struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   MCPServerSpec   `json:"spec,omitempty"`
	Status MCPServerStatus `json:"status,omitempty"`
}

type MCPServerList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`

	Items []MCPServer `json:"items"`
}

func (in *MCPServer) DeepCopyObject() runtime.Object {
	if in == nil {
		return nil
	}
	out := new(MCPServer)
	*out = *in
	out.ObjectMeta = *in.ObjectMeta.DeepCopy()
	if in.Spec.Args != nil {
		out.Spec.Args = append([]string(nil), in.Spec.Args...)
	}
	if in.Spec.Environment != nil {
		out.Spec.Environment = make(map[string]string, len(in.Spec.Environment))
		for key, value := range in.Spec.Environment {
			out.Spec.Environment[key] = value
		}
	}
	if in.Status.RegistrySyncedAt != nil {
		out.Status.RegistrySyncedAt = in.Status.RegistrySyncedAt.DeepCopy()
	}
	if in.Status.Conditions != nil {
		out.Status.Conditions = append([]metav1.Condition(nil), in.Status.Conditions...)
	}
	return out
}

func (in *MCPServerList) DeepCopyObject() runtime.Object {
	if in == nil {
		return nil
	}
	out := new(MCPServerList)
	*out = *in
	out.ListMeta = in.ListMeta
	if in.Items != nil {
		out.Items = make([]MCPServer, len(in.Items))
		for i := range in.Items {
			out.Items[i] = *in.Items[i].DeepCopyObject().(*MCPServer)
		}
	}
	return out
}
