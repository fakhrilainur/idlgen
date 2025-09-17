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
	"unicode"
)

// IDL represents the complete structure of a Solana IDL JSON file.
type IDL struct {
	Version      string                 `json:"version"`
	Name         string                 `json:"name"`
	Address      string                 `json:"address"`
	Metadata     IDLMetadata            `json:"metadata"`
	Accounts     []IdlAccountDefinition `json:"accounts"`
	Types        []IdlTypeDefinition    `json:"types"`
	Instructions []IdlInstruction       `json:"instructions"`
	Events       []IdlEvent             `json:"events"`
	Errors       []IdlError             `json:"errors"`
}

// IDLMetadata contains metadata about the IDL.
type IDLMetadata struct {
	Spec string `json:"spec"`
}

// IdlAccountDefinition represents an entry in the top-level "accounts" array of the IDL.
type IdlAccountDefinition struct {
	Name          string `json:"name"`
	Discriminator []int  `json:"discriminator"`
}

// IdlTypeDefinition represents a user-defined type in the "types" array of the IDL.
type IdlTypeDefinition struct {
	Name string `json:"name"`
	Type struct {
		Kind   string     `json:"kind"`
		Fields []IdlField `json:"fields"`
	} `json:"type"`
}

// IdlInstruction represents a single instruction in the IDL.
type IdlInstruction struct {
	Name     string       `json:"name"`
	Args     []IdlField   `json:"args"`
	Accounts []IdlAccount `json:"accounts"`
}

// IdlEvent represents an event in the IDL.
type IdlEvent struct {
	Name   string     `json:"name"`
	Fields []IdlField `json:"fields"`
}

// IdlError represents a custom program error.
type IdlError struct {
	Code    int    `json:"code"`
	Name    string `json:"name"`
	Message string `json:"msg"`
}

// IdlField represents a field in an instruction's args or a type's fields.
type IdlField struct {
	Name string  `json:"name"`
	Type IdlType `json:"type"`
}

// IdlAccount represents an account in an instruction's "accounts" list.
type IdlAccount struct {
	Name       string `json:"name"`
	IsWritable bool   `json:"writable"`
	IsSigner   bool   `json:"signer"`
}

// IdlType represents all possible type variations.
type IdlType struct {
	Primitive string          `json:"-"`
	Defined   *string         `json:"defined,omitempty"`
	Array     *[2]interface{} `json:"array,omitempty"`
	Vec       *interface{}    `json:"vec,omitempty"`
	Option    *interface{}    `json:"option,omitempty"`
	Coption   *interface{}    `json:"coption,omitempty"`
}

// UnmarshalJSON is a custom unmarshaler for IdlType.
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
	if coption, ok := obj["coption"]; ok {
		t.Coption = &coption
		return nil
	}
	return fmt.Errorf("unknown IDL type structure: %s", string(data))
}

// Template functions
var funcMap = template.FuncMap{
	"toPascalCase":             toPascalCase,
	"mapType":                  mapType,
	"bytesLiteral":             bytesLiteral,
	"accountDiscriminator":     accountDiscriminator,
	"instructionDiscriminator": instructionDiscriminator,
	"intSliceToBytesLiteral":   intSliceToBytesLiteral,
}

// --- Helper Functions ---
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

func toPascalCase(s string) string {
	s = strings.ReplaceAll(s, "_", " ")
	s = strings.ReplaceAll(s, "-", " ")
	s = strings.Title(s)
	return strings.ReplaceAll(s, " ", "")
}

func bytesLiteral(b []byte) string {
	parts := make([]string, len(b))
	for i, v := range b {
		parts[i] = fmt.Sprintf("0x%02x", v)
	}
	return strings.Join(parts, ", ")
}

func accountDiscriminator(name string) []byte {
	var result []rune
	for i, r := range name {
		if unicode.IsUpper(r) && i > 0 {
			result = append(result, '_')
		}
		result = append(result, unicode.ToLower(r))
	}
	h := sha256.Sum256([]byte("account:" + string(result)))
	return h[:8]
}

func instructionDiscriminator(name string) []byte {
	h := sha256.Sum256([]byte("global:" + name))
	return h[:8]
}

func mapType(t IdlType) string {
	if t.Primitive != "" {
		if t.Primitive == "pubkey" {
			return "solana.PublicKey"
		}
		switch t.Primitive {
		case "u8":
			return "uint8"
		case "i8":
			return "int8"
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
			return "*big.Int"
		case "i128":
			return "*big.Int"
		case "bool":
			return "bool"
		case "string":
			return "string"
		case "bytes":
			return "[]byte"
		case "publicKey":
			return "solana.PublicKey"
		default:
			return "interface{}"
		}
	}
	if t.Defined != nil {
		return toPascalCase(*t.Defined)
	}
	if t.Array != nil {
		innerTypeBytes, _ := json.Marshal((*t.Array)[0])
		var innerIdlType IdlType
		_ = json.Unmarshal(innerTypeBytes, &innerIdlType)
		size := (*t.Array)[1]
		return fmt.Sprintf("[%d]%s", int(size.(float64)), mapType(innerIdlType))
	}
	if t.Vec != nil {
		innerTypeBytes, _ := json.Marshal(*t.Vec)
		var innerIdlType IdlType
		_ = json.Unmarshal(innerTypeBytes, &innerIdlType)
		return fmt.Sprintf("[]%s", mapType(innerIdlType))
	}
	if t.Option != nil || t.Coption != nil {
		var inner interface{}
		if t.Option != nil {
			inner = *t.Option
		} else {
			inner = *t.Coption
		}
		innerTypeBytes, _ := json.Marshal(inner)
		var innerIdlType IdlType
		_ = json.Unmarshal(innerTypeBytes, &innerIdlType)
		return fmt.Sprintf("*%s", mapType(innerIdlType))
	}
	return "interface{}"
}

func Generate(idlPath, outPath, pkgName, clientName *string, verbose bool) error {

	if *idlPath == "" || *outPath == "" {
		log.Fatal("Usage: idlgen -idl <path.json> -out <path.go> [-pkg <name>] [-client <name>]")
	}

	data, err := os.ReadFile(*idlPath)
	if err != nil {
		log.Fatalf("Failed to read IDL file: %v", err)
	}

	var idl IDL
	if err := json.Unmarshal(data, &idl); err != nil {
		log.Fatalf("Failed to parse IDL: %v", err)
	}

	if idl.Name == "" {
		base := strings.TrimSuffix(filepath.Base(*idlPath), filepath.Ext(*idlPath))
		idl.Name = base
	}
	if *clientName == "" {
		*clientName = toPascalCase(idl.Name) + "Client"
	}

	typesMap := make(map[string]IdlTypeDefinition)
	for _, t := range idl.Types {
		typesMap[t.Name] = t
	}

	tmpl := template.Must(template.New("idl").Funcs(funcMap).Parse(goTemplate))

	var buf bytes.Buffer
	templateData := struct {
		PackageName string
		ClientName  string
		IDL         IDL
		TypesMap    map[string]IdlTypeDefinition
	}{
		PackageName: *pkgName,
		ClientName:  *clientName,
		IDL:         idl,
		TypesMap:    typesMap,
	}

	if err := tmpl.Execute(&buf, templateData); err != nil {
		log.Fatalf("Template execution failed: %v", err)
	}

	formatted, err := format.Source(buf.Bytes())
	if err != nil {
		log.Fatalf("Go format failed: %v\nRaw output:\n%s", err, buf.String())
	}

	outputDir := filepath.Dir(*outPath)
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		log.Fatalf("Failed to create output directory: %v", err)
	}
	if err := os.WriteFile(*outPath, formatted, 0644); err != nil {
		log.Fatalf("Failed to write output: %v", err)
	}
	if verbose {
		fmt.Printf("✅ Generated %s from %s\n", *outPath, *idlPath)
	}

	return nil
}

// --- Go Template ---
const goTemplate = `// Code generated by idlgen. DO NOT EDIT.
// Based on IDL: {{ .IDL.Name }}

package {{ .PackageName }}

import (
	"bytes"
	"errors"
	"fmt"

	"github.com/gagliardetto/binary"
	"github.com/gagliardetto/solana-go"
	"github.com/gagliardetto/solana-go/rpc"
)

{{ $prefix := .IDL.Name | toPascalCase -}}

// Program {{ $prefix }} address
var {{ $prefix }}ProgramID = solana.MustPublicKeyFromBase58("{{ .IDL.Address }}")

// --- Error Definitions ---
{{- range .IDL.Errors }}
// {{ $prefix }}{{ .Name | toPascalCase }}Error represents program error {{ .Code }}: {{ .Message }}
var {{ $prefix }}{{ .Name | toPascalCase }}Error = errors.New("{{ .Message }}")
{{- end }}

{{ $root := . -}}

// --- Struct Definitions ---
{{- range .IDL.Types }}
{{ $typeName := .Name | toPascalCase }}
// {{ $prefix }}{{ $typeName }} represents the {{ .Name }} type.
type {{ $prefix }}{{ $typeName }} struct {
	{{- range .Type.Fields }}
	{{ $fieldName := .Name | toPascalCase }}
	{{ $fieldType := mapType .Type -}}
	{{- if .Type.Defined -}}
		{{ $fieldName }} {{ $prefix }}{{ $fieldType }} ` + "`" + `bin:"{{ .Name }}"` + "`" + `
	{{- else -}}
		{{ $fieldName }} {{ $fieldType }} ` + "`" + `bin:"{{ .Name }}"` + "`" + `
	{{- end -}}
	{{- end }}
}
{{- end }}

// --- Account-specific Functions ---
{{- range .IDL.Accounts }}
{{ $accountName := .Name }}
{{ $typeName := .Name | toPascalCase }}

// {{ $prefix }}{{ $typeName }}Discriminator is the 8-byte discriminator for {{ .Name }} accounts.
var {{ $prefix }}{{ $typeName }}Discriminator = []byte{
{{- if .Discriminator }}
{{ intSliceToBytesLiteral .Discriminator }},
{{- else }}
{{ accountDiscriminator .Name | bytesLiteral }},
{{- end }}
}

// Decode{{ $prefix }}{{ $typeName }} deserializes account data into a {{ $prefix }}{{ $typeName }} struct.
func Decode{{ $prefix }}{{ $typeName }}(data []byte) (*{{ $prefix }}{{ $typeName }}, error) {
	if len(data) < 8 {
		return nil, fmt.Errorf("data too short for discriminator")
	}
	if !bytes.Equal(data[:8], {{ $prefix }}{{ $typeName }}Discriminator) {
		return nil, fmt.Errorf("invalid discriminator for {{ $prefix }}{{ $typeName }}: got %v, want %v", data[:8], {{ $prefix }}{{ $typeName }}Discriminator)
	}
	var account {{ $prefix }}{{ $typeName }}
	dec := bin.NewBorshDecoder(data[8:])
	if err := dec.Decode(&account); err != nil {
		return nil, fmt.Errorf("failed to decode {{ $prefix }}{{ $typeName }}: %w", err)
	}
	return &account, nil
}

// Serialize serializes the {{ $prefix }}{{ $typeName }} into a byte slice.
func (a *{{ $prefix }}{{ $typeName }}) Serialize() ([]byte, error) {
	var buf bytes.Buffer
	buf.Write({{ $prefix }}{{ $typeName }}Discriminator)
	enc := bin.NewBorshEncoder(&buf)
	if err := enc.Encode(a); err != nil {
		return nil, fmt.Errorf("failed to encode {{ $prefix }}{{ $typeName }}: %w", err)
	}
	return buf.Bytes(), nil
}
{{ end }}

// --- Instruction Builders ---
{{- range .IDL.Instructions }}
{{ $instrName := .Name | toPascalCase }}
// {{ $prefix }}{{ $instrName }}InstructionDiscriminator is the 8-byte discriminator for the "{{ .Name }}" instruction.
var {{ $prefix }}{{ $instrName }}InstructionDiscriminator = []byte{ {{ instructionDiscriminator .Name | bytesLiteral }} }

// {{ $prefix }}{{ $instrName }}Args represents the arguments for the "{{ .Name }}" instruction.
type {{ $prefix }}{{ $instrName }}Args struct {
	{{- range .Args }}
	{{ $fieldName := .Name | toPascalCase }}
	{{ $fieldType := mapType .Type -}}
	{{- if .Type.Defined -}}
		{{ $fieldName }} {{ $prefix }}{{ $fieldType }} ` + "`" + `bin:"{{ .Name }}"` + "`" + `
	{{- else -}}
		{{ $fieldName }} {{ $fieldType }} ` + "`" + `bin:"{{ .Name }}"` + "`" + `
	{{- end -}}
	{{- end }}
}

// {{ $prefix }}{{ $instrName }}Accounts represents the accounts for the "{{ .Name }}" instruction.
type {{ $prefix }}{{ $instrName }}Accounts struct {
	{{- range .Accounts }}
	{{ .Name | toPascalCase }} solana.PublicKey
	{{- end }}
}

// New{{ $prefix }}{{ $instrName }}Instruction creates a new "{{ .Name }}" instruction.
func New{{ $prefix }}{{ $instrName }}Instruction(
	args {{ $prefix }}{{ $instrName }}Args,
	accounts {{ $prefix }}{{ $instrName }}Accounts,
) (solana.Instruction, error) {
	// Serialize arguments.
	var buf bytes.Buffer
	buf.Write({{ $prefix }}{{ $instrName }}InstructionDiscriminator)
	enc := bin.NewBorshEncoder(&buf)
	if err := enc.Encode(args); err != nil {
		return nil, fmt.Errorf("failed to encode instruction args: %w", err)
	}

	// Build account metas.
	accountMetas := []*solana.AccountMeta{
		{{- range .Accounts }}
		{PublicKey: accounts.{{ .Name | toPascalCase }}, IsWritable: {{ .IsWritable }}, IsSigner: {{ .IsSigner }}},
		{{- end }}
	}

	instruction := solana.NewInstruction(
		{{ $prefix }}ProgramID,
		accountMetas,
		buf.Bytes(),
	)

	return instruction, nil
}
{{ end }}

// --- Program Client ---

// {{ .ClientName }} is a client for interacting with the {{ .IDL.Name }} program.
type {{ .ClientName }} struct {
	Connection *rpc.Client
	ProgramID  solana.PublicKey
}

// New{{ .ClientName }} creates a new client.
func New{{ .ClientName }}(rpcEndpoint string, programID solana.PublicKey) *{{ .ClientName }} {
	return &{{ .ClientName }}{
		Connection: rpc.New(rpcEndpoint),
		ProgramID:  programID,
	}
}

// --- Client Methods ---
{{- $clientName := .ClientName -}}
{{- range .IDL.Instructions }}
{{ $instrName := .Name | toPascalCase }}
{{ $methodName := .Name | toPascalCase }}

// {{ $methodName }} creates a transaction builder for the "{{ .Name }}" instruction.
func (c *{{ $clientName }}) {{ $methodName }}(
	args {{ $prefix }}{{ $instrName }}Args,
	accounts {{ $prefix }}{{ $instrName }}Accounts,
) *solana.TransactionBuilder {
	// Create the instruction.
	instruction, err := New{{ $prefix }}{{ $instrName }}Instruction(args, accounts)
	if err != nil {
		// This should never happen if the args are correct.
		panic(err)
	}
	
	return solana.NewTransactionBuilder().AddInstruction(instruction)
}
{{- end }}
`
