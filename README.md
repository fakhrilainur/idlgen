# Solana IDL Gen

A Solana IDL to Go bindings generator, similar to Ethereum's `abigen`.

## Features

- ✅ Generate Go bindings from Solana IDL JSON
- ✅ Support for accounts, instructions, events, and errors
- ✅ Type-safe argument and account structures
- ✅ Borsh serialization/deserialization
- ✅ Auto-generated account decoders with byte size validation
- ✅ Idiomatic Go naming conventions (PascalCase for types)
- ✅ Instruction discriminator handling (both legacy and new formats)
- ✅ Support for complex types (arrays, options, vectors)
- ✅ Client struct generation
- ✅ Comprehensive type mapping with byte size tracking

## Installation

```bash
go install github.com/fakhrilainur/idlgen@latest
```
## Usage

```bash
idlgen -idl examples/program.json -out examples/generated/program.go
```
