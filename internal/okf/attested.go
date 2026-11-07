package okf

import "gopkg.in/yaml.v3"

// Attested models an OKF v0.2 Attested Computation contract. agentmem only
// models the contract; it never executes or rewrites a sanctioned computation.
type Attested struct {
	Runtime     string
	Parameters  []Parameter
	Computation string
	Executor    *ExecutorSpec
	Attester    *AttesterSpec
}

// Parameter is a typed, named hole the agent may fill with a value only.
type Parameter struct {
	Name     string
	Type     string
	Required bool
}

// ExecutorSpec names run instructions and the receipt fields a run returns.
type ExecutorSpec struct {
	Resource string
	Receipt  []string
}

// AttesterSpec names deterministic (no-LLM) verification code.
type AttesterSpec struct {
	Resource string
}

func decodeAttestedKey(a *Attested, key string, v *yaml.Node) {
	switch key {
	case "runtime":
		a.Runtime = scalar(v)
	case "computation":
		a.Computation = scalar(v)
	case "parameters":
		if v == nil || v.Kind != yaml.SequenceNode {
			return
		}
		a.Parameters = nil
		for _, c := range v.Content {
			a.Parameters = append(a.Parameters, Parameter{
				Name:     scalar(mappingValue(c, "name")),
				Type:     scalar(mappingValue(c, "type")),
				Required: scalar(mappingValue(c, "required")) == "true",
			})
		}
	case "executor":
		a.Executor = &ExecutorSpec{
			Resource: scalar(mappingValue(v, "resource")),
			Receipt:  stringList(mappingValue(v, "receipt")),
		}
	case "attester":
		a.Attester = &AttesterSpec{Resource: scalar(mappingValue(v, "resource"))}
	}
}

// Validate checks the minimal contract of an Attested Computation.
func (a *Attested) Validate() error {
	if a == nil || a.Runtime == "" {
		return errMissingRuntime
	}
	return nil
}

var errMissingRuntime = &contractError{"okf: attested computation requires runtime"}

type contractError struct{ msg string }

func (e *contractError) Error() string { return e.msg }
