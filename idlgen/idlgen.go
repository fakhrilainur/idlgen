package idlgen

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"go/format"
	"log"
	"os"
	"path/filepath"
	"strings"
	"text/template"
)

// --- IDL Data Structures ---

// IDL represents the root structure of a Solana Program IDL.
type IDL struct {
	Version      string                 `json:"version"`
	Name         string                 `json:"name"`
	Address      string                 `json:"address"`
	Instructions []IdlInstruction       `json:"instructions"`
	Accounts     []IdlAccountDefinition `json:"accounts"`
	Types        []IdlTypeDefinition    `json:"types"`
	Errors       []IdlError             `json:"errors"`
}

// IdlInstruction represents a specific instruction definition.
type IdlInstruction struct {
	Name          string       `json:"name"`
	Docs          []string     `json:"docs"`
	Discriminator []int        `json:"discriminator"`
	Args          []IdlField   `json:"args"`
	Accounts      []IdlAccount `json:"accounts"`
}

// IdlAccountDefinition represents the definition of an account (mainly for discriminators).
type IdlAccountDefinition struct {
	Name          string `json:"name"`
	Discriminator []int  `json:"discriminator"`
}

// IdlTypeDefinition represents user-defined types (structs or enums).
type IdlTypeDefinition struct {
	Name string `json:"name"`
	Type struct {
		Kind     string       `json:"kind"` // "struct" or "enum"
		Fields   []IdlField   `json:"fields,omitempty"`
		Variants []IdlVariant `json:"variants,omitempty"`
	} `json:"type"`
}

// IdlVariant represents a specific variant within an Enum.
type IdlVariant struct {
	Name   string         `json:"name"`
	Fields []IdlEnumField `json:"fields,omitempty"`
}

// IdlEnumField represents a field within an Enum variant.
type IdlEnumField struct {
	Name string
	Type IdlType
}

// UnmarshalJSON handles custom deserialization for enum fields (named structs or tuple strings).
func (ef *IdlEnumField) UnmarshalJSON(data []byte) error {
	// Case 1: Primitive Type String
	var s string
	if err := json.Unmarshal(data, &s); err == nil {
		ef.Type = IdlType{Primitive: s}
		return nil
	}
	// Case 2: Object
	var m map[string]interface{}
	if err := json.Unmarshal(data, &m); err != nil {
		return err
	}
	_, hasName := m["name"]
	_, hasType := m["type"]
	if hasName && hasType {
		var f struct {
			Name string  `json:"name"`
			Type IdlType `json:"type"`
		}
		if err := json.Unmarshal(data, &f); err != nil {
			return err
		}
		ef.Name = f.Name
		ef.Type = f.Type
		return nil
	}
	var t IdlType
	if err := json.Unmarshal(data, &t); err != nil {
		return err
	}
	ef.Type = t
	return nil
}

// IdlField represents a standard field with a name and a type.
type IdlField struct {
	Name string  `json:"name"`
	Type IdlType `json:"type"`
}

// IdlAccount represents an account used in an instruction.
type IdlAccount struct {
	Name       string `json:"name"`
	IsWritable bool   `json:"writable"`
	IsSigner   bool   `json:"signer"`
}

// IdlError represents a custom program error.
type IdlError struct {
	Code    int    `json:"code"`
	Name    string `json:"name"`
	Message string `json:"msg"`
}

// IdlType represents polymorphic data types.
type IdlType struct {
	Primitive string
	Defined   *string
	Array     *[2]interface{}
	Vec       *interface{}
	Option    *interface{}
	Coption   *interface{}
}

// UnmarshalJSON handles polymorphism for IDL types.
func (t *IdlType) UnmarshalJSON(data []byte) error {
	var s string
	if err := json.Unmarshal(data, &s); err == nil {
		t.Primitive = s
		return nil
	}
	var obj map[string]interface{}
	if err := json.Unmarshal(data, &obj); err != nil {
		return err
	}
	if defined, ok := obj["defined"].(string); ok {
		t.Defined = &defined
		return nil
	}
	if definedObj, ok := obj["defined"].(map[string]interface{}); ok {
		if name, ok := definedObj["name"].(string); ok {
			t.Defined = &name
			return nil
		}
	}
	if array, ok := obj["array"].([]interface{}); ok && len(array) == 2 {
		t.Array = &[2]interface{}{array[0], array[1]}
		return nil
	}
	if vec, ok := obj["vec"]; ok {
		t.Vec = &vec
		return nil
	}
	if option, ok := obj["option"]; ok {
		t.Option = &option
		return nil
	}
	return nil
}

// --- Helper Functions ---

// toPascalCase converts a string to PascalCase.
func toPascalCase(s string) string {
	s = strings.ReplaceAll(s, "_", " ")
	s = strings.Title(s)
	return strings.ReplaceAll(s, " ", "")
}

// intSliceToBytesLiteral converts an int slice to a Go byte slice string.
func intSliceToBytesLiteral(nums []int) string {
	if len(nums) == 0 {
		return ""
	}
	parts := make([]string, len(nums))
	for i, v := range nums {
		parts[i] = fmt.Sprintf("0x%02x", v)
	}
	return strings.Join(parts, ", ")
}

// manualDiscriminator generates a discriminator hash if none is provided.
func manualDiscriminator(prefix, name string) string {
	h := sha256.Sum256([]byte(prefix + ":" + name))
	return intSliceToBytesLiteral([]int{int(h[0]), int(h[1]), int(h[2]), int(h[3]), int(h[4]), int(h[5]), int(h[6]), int(h[7])})
}

// --- Generator ---

// Generate processes the IDL and outputs the Go binding file.
func Generate(idlPath, outPath, pkgName, clientName *string, verbose bool) error {
	if *idlPath == "" || *outPath == "" {
		return fmt.Errorf("idl and out paths are required")
	}

	data, err := os.ReadFile(*idlPath)
	if err != nil {
		return err
	}

	var idl IDL
	if err := json.Unmarshal(data, &idl); err != nil {
		return fmt.Errorf("failed to parse IDL: %v", err)
	}

	if idl.Name == "" || idl.Name == "program" {
		fileName := filepath.Base(*idlPath)
		ext := filepath.Ext(fileName)
		idl.Name = strings.TrimSuffix(fileName, ext)
	}

	prefix := toPascalCase(idl.Name)

	if *clientName == "" {
		*clientName = prefix + "Client"
	}

	var mapType func(t IdlType) string
	mapType = func(t IdlType) string {
		if t.Primitive != "" {
			switch t.Primitive {
			case "bool":
				return "bool"
			case "u8", "i8":
				return "uint8"
			case "u16":
				return "uint16"
			case "i16":
				return "int16"
			case "u32":
				return "uint32"
			case "i32":
				return "int32"
			case "u64":
				return "uint64"
			case "i64":
				return "int64"
			case "u128":
				return "bin.Uint128"
			case "i128":
				return "bin.Int128"
			case "bytes":
				return "[]byte"
			case "string":
				return "string"
			case "pubkey", "publicKey":
				return "solana.PublicKey"
			default:
				return "interface{}"
			}
		}
		if t.Defined != nil {
			return prefix + toPascalCase(*t.Defined)
		}
		if t.Option != nil {
			innerBytes, _ := json.Marshal(*t.Option)
			var inner IdlType
			_ = json.Unmarshal(innerBytes, &inner)
			return "*" + mapType(inner)
		}
		if t.Vec != nil {
			innerBytes, _ := json.Marshal(*t.Vec)
			var inner IdlType
			_ = json.Unmarshal(innerBytes, &inner)
			return "[]" + mapType(inner)
		}
		if t.Array != nil {
			innerBytes, _ := json.Marshal((*t.Array)[0])
			var inner IdlType
			_ = json.Unmarshal(innerBytes, &inner)
			size := (*t.Array)[1]
			return fmt.Sprintf("[%d]%s", int(size.(float64)), mapType(inner))
		}
		return "interface{}"
	}

	funcMap := template.FuncMap{
		"toPascalCase":           toPascalCase,
		"mapType":                mapType,
		"intSliceToBytesLiteral": intSliceToBytesLiteral,
		"manualDiscriminator":    manualDiscriminator,
	}

	tmpl, err := template.New("idl").Funcs(funcMap).Parse(goTemplate)
	if err != nil {
		return err
	}

	var buf bytes.Buffer
	dataMap := struct {
		PackageName string
		ClientName  string
		Prefix      string
		IDL         IDL
	}{
		PackageName: *pkgName,
		ClientName:  *clientName,
		Prefix:      prefix,
		IDL:         idl,
	}

	if err := tmpl.Execute(&buf, dataMap); err != nil {
		return err
	}

	formatted, err := format.Source(buf.Bytes())
	if err != nil {
		if verbose {
			log.Printf("Warning: Code format failed: %v. Writing unformatted code.", err)
		}
		return os.WriteFile(*outPath, buf.Bytes(), 0644)
	}

	return os.WriteFile(*outPath, formatted, 0644)
}

// --- Template ---

const goTemplate = `// Code generated by idlgen. DO NOT EDIT.
// Program: {{ .IDL.Name }}

package {{ .PackageName }}

import (
	"bytes"
	"errors"
	"fmt"

	bin "github.com/gagliardetto/binary"
	"github.com/gagliardetto/solana-go"
	"github.com/gagliardetto/solana-go/rpc"
)

// ProgramID is the public key of the program.
var {{ .Prefix }}ProgramID = solana.MustPublicKeyFromBase58("{{ .IDL.Address }}")

// --- Errors ---
{{- range .IDL.Errors }}
// Err{{ $.Prefix }}{{ .Name | toPascalCase }} represents the error {{ .Name }}.
var Err{{ $.Prefix }}{{ .Name | toPascalCase }} = errors.New("{{ .Message }}")
{{- end }}

// --- Types ---
{{- range .IDL.Types }}
{{ $typeName := .Name | toPascalCase }}
{{- if eq .Type.Kind "struct" }}
// {{ $.Prefix }}{{ $typeName }} represents the struct {{ .Name }}.
type {{ $.Prefix }}{{ $typeName }} struct {
	{{- range .Type.Fields }}
	{{ .Name | toPascalCase }} {{ mapType .Type }} ` + "`" + `bin:"{{ .Name }}"` + "`" + `
	{{- end }}
}
{{- else if eq .Type.Kind "enum" }}
// Enum: {{ $.Prefix }}{{ $typeName }}
type {{ $.Prefix }}{{ $typeName }} = bin.BorshEnum
{{- end }}
{{- end }}

// --- Accounts ---
{{- range .IDL.Accounts }}
{{ $accName := .Name | toPascalCase }}
// {{ $.Prefix }}{{ $accName }}Discriminator is the discriminator for the account {{ .Name }}.
var {{ $.Prefix }}{{ $accName }}Discriminator = []byte{ {{ if .Discriminator }}{{ intSliceToBytesLiteral .Discriminator }}{{ else }}{{ manualDiscriminator "account" .Name }}{{ end }} }

// Note: The struct definition for account "{{ .Name }}" is generated in the Types section.
{{- end }}

// --- Instructions ---
{{- range .IDL.Instructions }}
{{ $instrName := .Name | toPascalCase }}

// {{ $.Prefix }}{{ $instrName }}Discriminator is the discriminator for instruction {{ .Name }}.
var {{ $.Prefix }}{{ $instrName }}Discriminator = []byte{ {{ if .Discriminator }}{{ intSliceToBytesLiteral .Discriminator }}{{ else }}{{ manualDiscriminator "global" .Name }}{{ end }} }

// {{ $.Prefix }}{{ $instrName }}Args represents the arguments for instruction {{ .Name }}.
type {{ $.Prefix }}{{ $instrName }}Args struct {
	{{- range .Args }}
	{{ .Name | toPascalCase }} {{ mapType .Type }} ` + "`" + `bin:"{{ .Name }}"` + "`" + `
	{{- end }}
}

// {{ $.Prefix }}{{ $instrName }}Accounts represents the accounts for instruction {{ .Name }}.
type {{ $.Prefix }}{{ $instrName }}Accounts struct {
	{{- range .Accounts }}
	{{ .Name | toPascalCase }} solana.PublicKey
	{{- end }}
}

// New{{ $.Prefix }}{{ $instrName }}Instruction creates a new instruction for {{ .Name }}.
func New{{ $.Prefix }}{{ $instrName }}Instruction(
	args {{ $.Prefix }}{{ $instrName }}Args,
	accounts {{ $.Prefix }}{{ $instrName }}Accounts,
) solana.Instruction {
	buf := new(bytes.Buffer)
	buf.Write({{ $.Prefix }}{{ $instrName }}Discriminator)
	encoder := bin.NewBorshEncoder(buf)
	if err := encoder.Encode(args); err != nil {
		panic(fmt.Errorf("failed to encode args: %w", err))
	}

	keys := []*solana.AccountMeta{
		{{- range .Accounts }}
		{
			PublicKey: accounts.{{ .Name | toPascalCase }},
			IsSigner:  {{ .IsSigner }},
			IsWritable: {{ .IsWritable }},
		},
		{{- end }}
	}

	return solana.NewInstruction(
		{{ $.Prefix }}ProgramID,
		keys,
		buf.Bytes(),
	)
}
{{- end }}

// --- Client ---

// {{ .ClientName }} provides easy access to program instructions.
type {{ .ClientName }} struct {
	Rpc *rpc.Client
}

// New{{ .ClientName }} creates a new instance of the client.
func New{{ .ClientName }}(endpoint string) *{{ .ClientName }} {
	return &{{ .ClientName }}{
		Rpc: rpc.New(endpoint),
	}
}
`
