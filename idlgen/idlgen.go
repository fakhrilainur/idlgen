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
	"strconv"
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
	Name           string           `json:"name"`
	GoName         string           `json:"-"`
	ArgsGoName     string           `json:"-"`
	AccountsGoName string           `json:"-"`
	EmitArgs       bool             `json:"-"`
	EmitAccounts   bool             `json:"-"`
	Skip           bool             `json:"-"`
	Docs           []string         `json:"docs"`
	Discriminator  []int            `json:"discriminator"`
	Discriminant   *IdlDiscriminant `json:"discriminant"`
	Args           []IdlField       `json:"args"`
	Accounts       []IdlAccount     `json:"accounts"`
}

// IdlDiscriminant represents legacy single-value instruction discriminants.
type IdlDiscriminant struct {
	Type  string `json:"type"`
	Value int    `json:"value"`
}

// IdlAccountDefinition represents the definition of an account (mainly for discriminators).
type IdlAccountDefinition struct {
	Name          string `json:"name"`
	GoName        string `json:"-"`
	Discriminator []int  `json:"discriminator"`
}

// IdlTypeDefinition represents user-defined types (structs or enums).
type IdlTypeDefinition struct {
	Name     string `json:"name"`
	GoName   string `json:"-"`
	ByteSize int    `json:"-"`
	Type     struct {
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
	var m map[string]any
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
	Name   string  `json:"name"`
	GoName string  `json:"-"`
	Type   IdlType `json:"type"`
}

// UnmarshalJSON handles named fields and tuple fields represented as bare types.
func (f *IdlField) UnmarshalJSON(data []byte) error {
	var named struct {
		Name string  `json:"name"`
		Type IdlType `json:"type"`
	}
	if err := json.Unmarshal(data, &named); err == nil && (named.Name != "" || named.Type != (IdlType{})) {
		f.Name = named.Name
		f.Type = named.Type
		return nil
	}

	var typ IdlType
	if err := json.Unmarshal(data, &typ); err != nil {
		return err
	}
	f.Type = typ
	return nil
}

// IdlAccount represents an account used in an instruction.
type IdlAccount struct {
	Name       string `json:"name"`
	GoName     string `json:"-"`
	IsWritable bool   `json:"writable"`
	IsSigner   bool   `json:"signer"`
}

// UnmarshalJSON handles Anchor and legacy account mutability field names.
func (a *IdlAccount) UnmarshalJSON(data []byte) error {
	var raw struct {
		Name       string `json:"name"`
		Writable   bool   `json:"writable"`
		Signer     bool   `json:"signer"`
		IsMut      bool   `json:"isMut"`
		IsSigner   bool   `json:"isSigner"`
		IsOptional bool   `json:"isOptional"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	a.Name = raw.Name
	a.IsWritable = raw.Writable || raw.IsMut
	a.IsSigner = raw.Signer || raw.IsSigner
	return nil
}

// IdlError represents a custom program error.
type IdlError struct {
	Code    int    `json:"code"`
	Name    string `json:"name"`
	GoName  string `json:"-"`
	Message string `json:"msg"`
}

// IdlType represents polymorphic data types.
type IdlType struct {
	Primitive string
	Defined   *string
	Array     *[2]any
	Vec       *any
	Option    *any
	Coption   *any
	Generic   *string
}

// UnmarshalJSON handles polymorphism for IDL types.
func (t *IdlType) UnmarshalJSON(data []byte) error {
	var s string
	if err := json.Unmarshal(data, &s); err == nil {
		t.Primitive = s
		return nil
	}
	var obj map[string]any
	if err := json.Unmarshal(data, &obj); err != nil {
		return err
	}
	if defined, ok := obj["defined"].(string); ok {
		t.Defined = &defined
		return nil
	}
	if definedObj, ok := obj["defined"].(map[string]any); ok {
		if name, ok := definedObj["name"].(string); ok {
			t.Defined = &name
			return nil
		}
	}
	if array, ok := obj["array"].([]any); ok && len(array) == 2 {
		t.Array = &[2]any{array[0], array[1]}
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
	if generic, ok := obj["generic"].(string); ok {
		t.Generic = &generic
		return nil
	}
	return nil
}

// --- Helper Functions ---

// toPascalCase converts a string to PascalCase.
func toPascalCase(s string) string {
	var b strings.Builder
	capitalizeNext := true
	for _, r := range s {
		isAlphaNum := r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9'
		if !isAlphaNum {
			capitalizeNext = true
			continue
		}
		if capitalizeNext && r >= 'a' && r <= 'z' {
			r -= 'a' - 'A'
		}
		b.WriteRune(r)
		capitalizeNext = false
	}
	return b.String()
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

func instructionDiscriminatorBytes(in IdlInstruction) []int {
	if len(in.Discriminator) > 0 {
		return in.Discriminator
	}
	if in.Discriminant == nil {
		return nil
	}

	value := in.Discriminant.Value
	switch in.Discriminant.Type {
	case "u16":
		return []int{value & 0xff, (value >> 8) & 0xff}
	case "u32":
		return []int{value & 0xff, (value >> 8) & 0xff, (value >> 16) & 0xff, (value >> 24) & 0xff}
	case "u64":
		return []int{value & 0xff, (value >> 8) & 0xff, (value >> 16) & 0xff, (value >> 24) & 0xff, (value >> 32) & 0xff, (value >> 40) & 0xff, (value >> 48) & 0xff, (value >> 56) & 0xff}
	default:
		return []int{value & 0xff}
	}
}

func instructionDiscriminatorLiteral(in IdlInstruction) string {
	if bytes := instructionDiscriminatorBytes(in); len(bytes) > 0 {
		return intSliceToBytesLiteral(bytes)
	}
	return manualDiscriminator("global", in.Name)
}

func instructionDiscriminatorSuffix(in IdlInstruction, fallback int) string {
	if in.Discriminant != nil {
		return fmt.Sprintf("Discriminant%d", in.Discriminant.Value)
	}
	if len(in.Discriminator) > 0 {
		parts := make([]string, len(in.Discriminator))
		for i, b := range in.Discriminator {
			parts[i] = fmt.Sprintf("%02X", b)
		}
		return "Discriminator" + strings.Join(parts, "")
	}
	return fmt.Sprintf("Variant%d", fallback)
}

func instructionShapeKey(in IdlInstruction) string {
	data, _ := json.Marshal(struct {
		Args     []IdlField
		Accounts []IdlAccount
	}{
		Args:     in.Args,
		Accounts: in.Accounts,
	})
	return string(data)
}

func instructionSemanticKey(in IdlInstruction) string {
	data, _ := json.Marshal(struct {
		Name          string
		Discriminator []int
		Shape         string
	}{
		Name:          in.Name,
		Discriminator: instructionDiscriminatorBytes(in),
		Shape:         instructionShapeKey(in),
	})
	return string(data)
}

func uniqueGoName(name string, used map[string]int) string {
	base := toPascalCase(name)
	if base == "" {
		base = "Unnamed"
	}
	if base[0] >= '0' && base[0] <= '9' {
		base = "Field" + base
	}

	count := used[base]
	used[base] = count + 1
	if count == 0 {
		return base
	}
	return fmt.Sprintf("%s%d", base, count+1)
}

func assignFieldGoNames(fields []IdlField) {
	used := make(map[string]int)
	for i := range fields {
		name := fields[i].Name
		if name == "" {
			name = fmt.Sprintf("field%d", i)
		}
		fields[i].GoName = uniqueGoName(name, used)
	}
}

func assignAccountGoNames(accounts []IdlAccount) {
	used := make(map[string]int)
	for i := range accounts {
		accounts[i].GoName = uniqueGoName(accounts[i].Name, used)
	}
}

func assignGoNames(idl *IDL) {
	usedTypes := make(map[string]int)
	for i := range idl.Types {
		idl.Types[i].GoName = uniqueGoName(idl.Types[i].Name, usedTypes)
		assignFieldGoNames(idl.Types[i].Type.Fields)
	}

	usedAccounts := make(map[string]int)
	for i := range idl.Accounts {
		idl.Accounts[i].GoName = uniqueGoName(idl.Accounts[i].Name, usedAccounts)
	}

	for i := range idl.Instructions {
		assignFieldGoNames(idl.Instructions[i].Args)
		assignAccountGoNames(idl.Instructions[i].Accounts)
	}
	assignInstructionGoNames(idl.Instructions)

	usedErrors := make(map[string]int)
	for i := range idl.Errors {
		idl.Errors[i].GoName = uniqueGoName(idl.Errors[i].Name, usedErrors)
	}
}

func assignInstructionGoNames(instructions []IdlInstruction) {
	groups := make(map[string][]int)
	for i := range instructions {
		base := toPascalCase(instructions[i].Name)
		if base == "" {
			base = "Instruction"
		}
		groups[base] = append(groups[base], i)
	}

	for base, indexes := range groups {
		seenSemantic := make(map[string]bool)
		shapeCounts := make(map[string]int)
		for _, idx := range indexes {
			key := instructionSemanticKey(instructions[idx])
			if seenSemantic[key] {
				instructions[idx].Skip = true
				continue
			}
			seenSemantic[key] = true
			shapeCounts[instructionShapeKey(instructions[idx])]++
		}

		usedTypeShapes := make(map[string]bool)
		variant := 1
		for _, idx := range indexes {
			if instructions[idx].Skip {
				continue
			}

			goName := base
			if len(indexes) > 1 {
				goName = base + instructionDiscriminatorSuffix(instructions[idx], variant)
			}
			instructions[idx].GoName = goName

			shapeKey := instructionShapeKey(instructions[idx])
			if shapeCounts[shapeKey] > 1 {
				instructions[idx].ArgsGoName = base
				instructions[idx].AccountsGoName = base
			} else {
				instructions[idx].ArgsGoName = goName
				instructions[idx].AccountsGoName = goName
			}

			if !usedTypeShapes["args:"+instructions[idx].ArgsGoName] {
				instructions[idx].EmitArgs = true
				usedTypeShapes["args:"+instructions[idx].ArgsGoName] = true
			}
			if !usedTypeShapes["accounts:"+instructions[idx].AccountsGoName] {
				instructions[idx].EmitAccounts = true
				usedTypeShapes["accounts:"+instructions[idx].AccountsGoName] = true
			}
			variant++
		}
	}
}

func quoteGoString(s string) string {
	return strconv.Quote(s)
}

func shouldGenerateDecodeHelper(t IdlTypeDefinition) bool {
	return t.Type.Kind == "struct" && t.ByteSize > 0 && !strings.HasSuffix(t.GoName, "Params")
}

func typeByteSize(t IdlType, typeSizes map[string]int) (int, bool) {
	if t.Primitive != "" {
		switch t.Primitive {
		case "bool", "u8", "i8":
			return 1, true
		case "u16", "i16":
			return 2, true
		case "u32", "i32":
			return 4, true
		case "u64", "i64":
			return 8, true
		case "u128", "i128":
			return 16, true
		case "pubkey", "publicKey":
			return 32, true
		case "string", "bytes":
			return 0, false
		default:
			return 0, false
		}
	}
	if t.Defined != nil {
		size, ok := typeSizes[*t.Defined]
		return size, ok
	}
	if t.Array != nil {
		innerBytes, _ := json.Marshal((*t.Array)[0])
		var inner IdlType
		if err := json.Unmarshal(innerBytes, &inner); err != nil {
			return 0, false
		}
		innerSize, ok := typeByteSize(inner, typeSizes)
		if !ok {
			return 0, false
		}
		size, ok := (*t.Array)[1].(float64)
		if !ok {
			return 0, false
		}
		return innerSize * int(size), true
	}
	return 0, false
}

func assignTypeByteSizes(idl *IDL) {
	typeSizes := make(map[string]int)
	for _, typ := range idl.Types {
		if typ.Type.Kind == "enum" {
			typeSizes[typ.Name] = 1
		}
	}

	for {
		changed := false
		for i := range idl.Types {
			if _, ok := typeSizes[idl.Types[i].Name]; ok {
				continue
			}
			if idl.Types[i].Type.Kind != "struct" {
				continue
			}

			size := 0
			ok := true
			for _, field := range idl.Types[i].Type.Fields {
				fieldSize, fieldOK := typeByteSize(field.Type, typeSizes)
				if !fieldOK {
					ok = false
					break
				}
				size += fieldSize
			}
			if ok {
				idl.Types[i].ByteSize = size
				typeSizes[idl.Types[i].Name] = size
				changed = true
			}
		}
		if !changed {
			break
		}
	}
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
	assignGoNames(&idl)
	assignTypeByteSizes(&idl)

	prefix := toPascalCase(idl.Name)
	typeNames := make(map[string]string, len(idl.Types))
	for _, typ := range idl.Types {
		if _, ok := typeNames[typ.Name]; !ok {
			typeNames[typ.Name] = typ.GoName
		}
	}

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
				return "any"
			}
		}
		if t.Defined != nil {
			if goName, ok := typeNames[*t.Defined]; ok {
				return prefix + goName
			}
			return prefix + toPascalCase(*t.Defined)
		}
		if t.Option != nil {
			innerBytes, _ := json.Marshal(*t.Option)
			var inner IdlType
			_ = json.Unmarshal(innerBytes, &inner)
			return "*" + mapType(inner)
		}
		if t.Coption != nil {
			innerBytes, _ := json.Marshal(*t.Coption)
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
			if n, ok := size.(float64); ok {
				return fmt.Sprintf("[%d]%s", int(n), mapType(inner))
			}
			return "[]" + mapType(inner)
		}
		if t.Generic != nil {
			return "any"
		}
		return "any"
	}

	funcMap := template.FuncMap{
		"toPascalCase":                    toPascalCase,
		"mapType":                         mapType,
		"shouldGenerateDecodeHelper":      shouldGenerateDecodeHelper,
		"quoteGoString":                   quoteGoString,
		"instructionDiscriminatorLiteral": instructionDiscriminatorLiteral,
		"intSliceToBytesLiteral":          intSliceToBytesLiteral,
		"manualDiscriminator":             manualDiscriminator,
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
		NeedsErrors bool
		IDL         IDL
	}{
		PackageName: *pkgName,
		ClientName:  *clientName,
		Prefix:      prefix,
		NeedsErrors: len(idl.Errors) > 0,
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
	{{- if .NeedsErrors }}
	"errors"
	{{- end }}
	"fmt"

	bin "github.com/gagliardetto/binary"
	"github.com/gagliardetto/solana-go"
	"github.com/gagliardetto/solana-go/rpc"
)

// ProgramID is the public key of the program.
{{- if .IDL.Address }}
var {{ .Prefix }}ProgramID = solana.MustPublicKeyFromBase58("{{ .IDL.Address }}")
{{- else }}
var {{ .Prefix }}ProgramID solana.PublicKey
{{- end }}

// --- Errors ---
{{- range .IDL.Errors }}
// Err{{ $.Prefix }}{{ .GoName }} represents the error {{ .Name }}.
var Err{{ $.Prefix }}{{ .GoName }} = errors.New({{ quoteGoString .Message }})
{{- end }}

// --- Types ---
{{- range .IDL.Types }}
{{ $typeName := .GoName }}
{{- if eq .Type.Kind "struct" }}
// {{ $.Prefix }}{{ $typeName }} represents the struct {{ .Name }}.
type {{ $.Prefix }}{{ $typeName }} struct {
	{{- range .Type.Fields }}
	{{ .GoName }} {{ mapType .Type }} ` + "`" + `bin:"{{ .Name }}"` + "`" + `
	{{- end }}
}
{{- if .ByteSize }}
// {{ $.Prefix }}{{ $typeName }}Size is the fixed Borsh byte size for {{ .Name }}.
const {{ $.Prefix }}{{ $typeName }}Size = {{ .ByteSize }}
{{- end }}
{{- if shouldGenerateDecodeHelper . }}

// Decode{{ $.Prefix }}{{ $typeName }} decodes {{ $.Prefix }}{{ $typeName }} from Borsh bytes.
func Decode{{ $.Prefix }}{{ $typeName }}(data []byte) ({{ $.Prefix }}{{ $typeName }}, error) {
	if len(data) < {{ $.Prefix }}{{ $typeName }}Size {
		return {{ $.Prefix }}{{ $typeName }}{}, fmt.Errorf("data too short: %d, expected at least %d", len(data), {{ $.Prefix }}{{ $typeName }}Size)
	}

	var out {{ $.Prefix }}{{ $typeName }}
	decoder := bin.NewBorshDecoder(data)
	if err := decoder.Decode(&out); err != nil {
		return {{ $.Prefix }}{{ $typeName }}{}, fmt.Errorf("decode {{ $.Prefix }}{{ $typeName }} failed: %w", err)
	}
	return out, nil
}
{{- end }}
{{- else if eq .Type.Kind "enum" }}
// Enum: {{ $.Prefix }}{{ $typeName }}
type {{ $.Prefix }}{{ $typeName }} = bin.BorshEnum
{{- end }}
{{- end }}

// --- Accounts ---
{{- range .IDL.Accounts }}
{{ $accName := .GoName }}
// {{ $.Prefix }}{{ $accName }}Discriminator is the discriminator for the account {{ .Name }}.
var {{ $.Prefix }}{{ $accName }}Discriminator = []byte{ {{ if .Discriminator }}{{ intSliceToBytesLiteral .Discriminator }}{{ else }}{{ manualDiscriminator "account" .Name }}{{ end }} }

// Note: The struct definition for account "{{ .Name }}" is generated in the Types section.
{{- end }}

// --- Instructions ---
{{- range .IDL.Instructions }}
{{- if not .Skip }}
{{ $instrName := .GoName }}

// {{ $.Prefix }}{{ $instrName }}Discriminator is the discriminator for instruction {{ .Name }}.
var {{ $.Prefix }}{{ $instrName }}Discriminator = []byte{ {{ instructionDiscriminatorLiteral . }} }

{{- if .EmitArgs }}
// {{ $.Prefix }}{{ $instrName }}Args represents the arguments for instruction {{ .Name }}.
type {{ $.Prefix }}{{ .ArgsGoName }}Args struct {
	{{- range .Args }}
	{{ .GoName }} {{ mapType .Type }} ` + "`" + `bin:"{{ .Name }}"` + "`" + `
	{{- end }}
}
{{- end }}

{{- if .EmitAccounts }}
// {{ $.Prefix }}{{ $instrName }}Accounts represents the accounts for instruction {{ .Name }}.
type {{ $.Prefix }}{{ .AccountsGoName }}Accounts struct {
	{{- range .Accounts }}
	{{ .GoName }} solana.PublicKey
	{{- end }}
}
{{- end }}

// New{{ $.Prefix }}{{ $instrName }}Instruction creates a new instruction for {{ .Name }}.
func New{{ $.Prefix }}{{ $instrName }}Instruction(
	args {{ $.Prefix }}{{ .ArgsGoName }}Args,
	accounts {{ $.Prefix }}{{ .AccountsGoName }}Accounts,
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
			PublicKey: accounts.{{ .GoName }},
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
