package main

import (
	"context"
	"flag"
	"log"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"

	"github.com/hiranadikari/terraform-provider-vyos/internal/provider"
)

// version is set at release time by goreleaser via -ldflags.
var version = "dev"

func main() {
	var debug bool
	flag.BoolVar(&debug, "debug", false, "Enable provider debug mode (for use with Terraform debugger).")
	flag.Parse()

	opts := providerserver.ServeOpts{
		Address: "registry.terraform.io/hiranadikari/vyos",
		Debug:   debug,
	}

	if err := providerserver.Serve(context.Background(), provider.New(version), opts); err != nil {
		log.Fatal(err)
	}
}
