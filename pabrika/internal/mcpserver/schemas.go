package mcpserver

import "github.com/RJGJ/Pabrika/internal/service"

// Schema helpers. Schemas are plain maps built once at init from the shared service
// constants; additionalProperties is always false.

type m = map[string]any

func objSchema(props m, required ...string) m {
	s := m{"type": "object", "properties": props, "additionalProperties": false}
	if len(required) > 0 {
		req := make([]any, len(required))
		for i, r := range required {
			req[i] = r
		}
		s["required"] = req
	}
	return s
}

func strProp(desc string, max int) m {
	p := m{"type": "string", "description": desc}
	if max > 0 {
		p["maxLength"] = max
	}
	return p
}

func nullableStrProp(desc string, max int) m {
	p := strProp(desc, max)
	p["type"] = []any{"string", "null"}
	return p
}

func enumProp(desc string, vals []string) m {
	e := make([]any, len(vals))
	for i, v := range vals {
		e[i] = v
	}
	return m{"type": "string", "description": desc, "enum": e}
}

func boolProp(desc string) m { return m{"type": "boolean", "description": desc} }

func intProp(desc string, min, max int) m {
	return m{"type": "integer", "description": desc, "minimum": min, "maximum": max}
}

func statusNames() []string {
	out := make([]string, len(service.Statuses))
	for i, s := range service.Statuses {
		out[i] = string(s)
	}
	return out
}

func priorityNames() []string {
	out := make([]string, len(service.Priorities))
	for i, s := range service.Priorities {
		out[i] = string(s)
	}
	return out
}

func colorNames() []string {
	out := make([]string, len(service.LabelColors))
	for i, s := range service.LabelColors {
		out[i] = string(s)
	}
	return out
}

var (
	projectProp = strProp("Project key (for example WEB) or project id.", 0)
	ticketProp  = strProp("Ticket reference (for example WEB-12) or ticket id.", 0)
)

func labelsProp() m {
	return m{"type": "array", "description": "Label names that already exist in the project.",
		"items": m{"type": "string", "maxLength": service.MaxLabelName}, "maxItems": service.MaxLabelsPerTicket}
}
