package okf

import (
	"fmt"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

// Attested models an OKF v0.2 Attested Computation contract. The OKF layer
// only declares the sanctioned computation; execution, receipt verification
// and rejection are implemented in internal/compute.
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

var allowedParamTypes = map[string]bool{
	"integer": true, "number": true, "string": true, "boolean": true, "date": true,
}

// blockFence matches a fenced block and captures its info string and body.
// The closing fence may be any fence: extraction is permissive on purpose.
var blockFence = regexp.MustCompile("(?s)```([A-Za-z0-9_+.-]*)[ \t]*\r?\n(.*?)```")

// RuntimeID is the normalised runtime name used by the runtime registry.
func (a *Attested) RuntimeID() string {
	if a == nil {
		return ""
	}
	return strings.ToLower(strings.TrimSpace(a.Runtime))
}

// ReceiptFields names the evidence an executor must return. Optional: a
// missing executor still parses correctly (OKF conformance stays permissive).
func (a *Attested) ReceiptFields() []string {
	if a == nil || a.Executor == nil {
		return nil
	}
	return append([]string(nil), a.Executor.Receipt...)
}

// ValidParameterTypes lists the types the binder accepts.
func ValidParameterTypes() []string {
	out := make([]string, 0, len(allowedParamTypes))
	for t := range allowedParamTypes {
		out = append(out, t)
	}
	return out
}

// ExtractInlineBlock returns the first fenced block whose info string matches
// one of langs (case-insensitive). It is used when `computation` is absent or
// set to `inline`; the language list is chosen by the runtime, because a
// python script or a dbt selector is as legitimate a sanctioned body as SQL.
func ExtractInlineBlock(body string, langs ...string) string {
	want := make(map[string]bool, len(langs))
	for _, l := range langs {
		want[strings.ToLower(strings.TrimSpace(l))] = true
	}
	for _, m := range blockFence.FindAllStringSubmatch(body, -1) {
		if want[strings.ToLower(m[1])] {
			return strings.TrimSpace(m[2])
		}
	}
	return ""
}

// ExtractInlineSQL returns the first fenced ```sql block in a document body.
func ExtractInlineSQL(body string) string {
	return ExtractInlineBlock(body, "sql")
}

// Validate checks an Attested Computation contract before execution. It is
// never called while parsing a bundle, so strictness here does not affect
// permissive OKF conformance.
func (a *Attested) Validate() error {
	if a == nil || a.Runtime == "" {
		return errMissingRuntime
	}
	if strings.TrimSpace(a.Computation) == "" {
		// `computation: inline` selects the fenced ```sql block in the body,
		// otherwise it is a bundle-relative resource path.
		return errMissingComputation
	}
	seen := map[string]bool{}
	for _, p := range a.Parameters {
		name := strings.TrimSpace(p.Name)
		if name == "" || seen[name] {
			return fmt.Errorf("okf: duplicate or empty parameter name %q", p.Name)
		}
		seen[name] = true
		if !allowedParamTypes[strings.ToLower(strings.TrimSpace(p.Type))] {
			return fmt.Errorf("okf: parameter %q has unsupported type %q", p.Name, p.Type)
		}
	}
	return nil
}

var (
	errMissingRuntime     = &contractError{"okf: attested computation requires runtime"}
	errMissingComputation = &contractError{"okf: attested computation requires computation"}
)

type contractError struct{ msg string }

func (e *contractError) Error() string { return e.msg }
