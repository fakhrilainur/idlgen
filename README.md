# Solana IDL Gen

A Solana IDL to Go bindings generator, similar to Ethereum's `abigen`.

## Features

- ✅ Generate Go bindings from Solana IDL JSON
- ✅ Support for accounts, instructions, events, and errors
- ✅ Type-safe argument and account structures
- ✅ Borsh serialization/deserialization
- ✅ Client struct generation
- ✅ Comprehensive type mapping

## Installation

```bash
go install github.com/fakhrilainur/idlgen@latest
```
## Usage

```bash
idlgen -idl examples/program.json -out examples/generated/program.go
```
