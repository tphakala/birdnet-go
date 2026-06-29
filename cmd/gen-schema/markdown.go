package main

import (
	"fmt"
	"strings"

	"github.com/invopop/jsonschema"
)

const markdownHeader = `# Configuration Reference

> Auto-generated from source. Do not edit manually.
>
> For IDE autocomplete, add to the top of your ` + "`config.yaml`" + `:
>
> ` + "```yaml" + `
> # yaml-language-server: $schema=https://raw.githubusercontent.com/tphakala/birdnet-go/main/config.schema.json
> ` + "```" + `

`

// renderMarkdown walks the schema and produces a flat markdown reference
// grouped by top-level config section.
func renderMarkdown(schema *jsonschema.Schema) string {
	var b strings.Builder
	b.WriteString(markdownHeader)

	settingsDef := resolveRefWithDesc(schema, schema)
	if settingsDef == nil || settingsDef.Properties == nil {
		return b.String()
	}

	for pair := settingsDef.Properties.Oldest(); pair != nil; pair = pair.Next() {
		key := pair.Key
		prop := pair.Value

		resolved := resolveRefWithDesc(prop, schema)
		if resolved == nil {
			continue
		}

		if isScalar(resolved) {
			writeSubSection(&b, key, resolved)
			continue
		}

		fmt.Fprintf(&b, "## %s\n\n", key)
		if resolved.Description != "" {
			b.WriteString(resolved.Description + "\n\n")
		}

		b.WriteString("| Setting | Type | Description |\n")
		b.WriteString("|---------|------|-------------|\n")

		flattenProperties(&b, key, resolved, schema)
		b.WriteByte('\n')
	}

	return b.String()
}

func writeSubSection(b *strings.Builder, key string, prop *jsonschema.Schema) {
	fmt.Fprintf(b, "## %s\n\n", key)
	b.WriteString("| Setting | Type | Description |\n")
	b.WriteString("|---------|------|-------------|\n")
	fmt.Fprintf(b, "| `%s` | %s | %s |\n\n", key, schemaType(prop), escapeCell(prop.Description))
}

// flattenProperties recursively flattens nested properties into dotted paths.
func flattenProperties(b *strings.Builder, prefix string, s, root *jsonschema.Schema) {
	if s.Properties == nil {
		return
	}

	for pair := s.Properties.Oldest(); pair != nil; pair = pair.Next() {
		key := pair.Key
		prop := pair.Value
		fullKey := prefix + "." + key

		resolved := resolveRefWithDesc(prop, root)
		if resolved == nil {
			continue
		}

		if isObject(resolved) && !isMap(resolved) {
			flattenProperties(b, fullKey, resolved, root)
			continue
		}

		typeName := schemaType(resolved)
		desc := escapeCell(resolved.Description)
		fmt.Fprintf(b, "| `%s` | %s | %s |\n", fullKey, typeName, desc)
	}
}

// resolveRefWithDesc follows a $ref to its definition. When the reference site
// carries a description that the definition lacks, it returns a shallow copy
// with the description filled in — avoiding mutation of the shared definition.
func resolveRefWithDesc(s, root *jsonschema.Schema) *jsonschema.Schema {
	if s == nil {
		return nil
	}
	if s.Ref == "" || root.Definitions == nil {
		return s
	}
	refName := strings.TrimPrefix(s.Ref, "#/$defs/")
	def, ok := root.Definitions[refName]
	if !ok {
		return s
	}
	if s.Description != "" && def.Description == "" {
		patched := *def
		patched.Description = s.Description
		return &patched
	}
	return def
}

func isScalar(s *jsonschema.Schema) bool {
	if s.Properties != nil {
		return false
	}
	return s.Type == "string" || s.Type == "boolean" || s.Type == "integer" || s.Type == "number"
}

func isObject(s *jsonschema.Schema) bool {
	return s.Properties != nil
}

func isMap(s *jsonschema.Schema) bool {
	return s.AdditionalProperties != nil && s.Properties == nil
}

func schemaType(s *jsonschema.Schema) string {
	if s.Ref != "" {
		return "object"
	}

	if s.Type == "array" {
		if s.Items != nil {
			itemType := "any"
			if s.Items.Type != "" {
				itemType = s.Items.Type
			} else if s.Items.Ref != "" {
				refName := strings.TrimPrefix(s.Items.Ref, "#/$defs/")
				itemType = camelToKebab(refName)
			}
			return itemType + "[]"
		}
		return "array"
	}

	if s.Type != "" {
		return s.Type
	}

	if s.Properties != nil {
		return "object"
	}

	return "any"
}

func camelToKebab(s string) string {
	var b strings.Builder
	for i, r := range s {
		if r >= 'A' && r <= 'Z' {
			if i > 0 {
				b.WriteByte('-')
			}
			b.WriteRune(r + 32)
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func escapeCell(s string) string {
	s = strings.ReplaceAll(s, "|", "\\|")
	s = strings.ReplaceAll(s, "\n", " ")
	return s
}
