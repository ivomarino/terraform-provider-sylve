// terraform-provider-sylve is a Terraform provider for Sylve
// (https://sylve.io/), a management plane for bhyve VMs and jails on
// FreeBSD.
package main

import (
	"context"
	"flag"
	"log"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"

	"github.com/ivomarino/terraform-provider-sylve/internal/provider"
)

// version is overridden at build time via -ldflags "-X main.version=...".
var version = "dev"

func main() {
	var debug bool
	flag.BoolVar(&debug, "debug", false, "run the provider with support for debuggers like delve")
	flag.Parse()

	err := providerserver.Serve(context.Background(), provider.New(version), providerserver.ServeOpts{
		Address: "registry.terraform.io/ivomarino/sylve",
		Debug:   debug,
	})
	if err != nil {
		log.Fatal(err.Error())
	}
}
