package main

import (
	"flag"
	"log"

	"github.com/fakhrilainur/idlgen/idlgen"
)

func main() {
	var (
		idlPath    = flag.String("idl", "", "Path to the IDL JSON file")
		outPath    = flag.String("out", "", "Path to the output Go file")
		pkgName    = flag.String("pkg", "main", "Go package name")
		clientName = flag.String("client", "", "Client struct name (optional)")
		verbose    = flag.Bool("v", false, "Verbose output")
	)
	flag.Parse()

	err := idlgen.Generate(idlPath, outPath, pkgName, clientName, *verbose)
	if err != nil {
		log.Fatal(err)
	}
}
