package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

const (
	MCPServerKind = "MCPServer"

	// ConditionAccepted reports whether the spec passed validation.
	ConditionAccepted = "Accepted"
	// ConditionReady reports whether the Gateway can route to the server.
	ConditionReady = "Ready"
	// ConditionToolsDiscovered reports whether the Operator listed the
	// server's tools. It never affects Ready.
	ConditionToolsDiscovered = "ToolsDiscovered"

	ReasonValid                = "Valid"
	ReasonInvalidSpec          = "InvalidSpec"
	ReasonRoutable             = "Routable"
	ReasonUnsupportedTransport = "UnsupportedTransport"
	ReasonNamespaceNotWatched  = "NamespaceNotWatched"
	ReasonDiscovered           = "Discovered"
	ReasonDiscoveryFailed      = "DiscoveryFailed"
	ReasonNotRoutable          = "NotRoutable"

	// DescriptionAnnotation holds an optional human-readable description.
	DescriptionAnnotation = "kavrynt.io/description"
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
	Conditions         []metav1.Condition `json:"conditions,omitempty"`
	// Tools are the server's tools from MCP tools/list, bounded by the
	// Operator. They come from the MCP server and are untrusted.
	Tools []MCPServerTool `json:"tools,omitempty"`
}

// MCPServerTool is one tool reported by an MCP server.
type MCPServerTool struct {
	Name        string                `json:"name"`
	Description string                `json:"description,omitempty"`
	InputSchema *runtime.RawExtension `json:"inputSchema,omitempty"`
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
	if in.Status.Conditions != nil {
		out.Status.Conditions = append([]metav1.Condition(nil), in.Status.Conditions...)
	}
	out.Status.Tools = CopyTools(in.Status.Tools)
	return out
}

// CopyTools returns a deep copy of tools.
func CopyTools(tools []MCPServerTool) []MCPServerTool {
	if tools == nil {
		return nil
	}
	out := make([]MCPServerTool, len(tools))
	for i, tool := range tools {
		out[i] = tool
		if tool.InputSchema != nil {
			out[i].InputSchema = tool.InputSchema.DeepCopy()
		}
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

// RouteName is the stable Gateway route identifier: <namespace>.<name>.
func (in *MCPServer) RouteName() string {
	if in.Namespace == "" {
		return in.Name
	}
	return in.Namespace + "." + in.Name
}
