// The wpengine OpenTofu provider: declares what is Metatavu's concern about a
// WordPress project on WP Engine.
package main

import (
	"context"
	"flag"
	"log"

	"github.com/Metatavu/metatavu-wpengine-opentofu/internal/provider"
	"github.com/hashicorp/terraform-plugin-framework/providerserver"
)

// Address is the provider's source address. It isn't in a public registry:
// it's installed from a filesystem mirror (README.md).
const Address = "metatavu.io/metatavu/wpengine"

// version is set by the release build.
var version = "dev"

func main() {
	var debug bool
	flag.BoolVar(&debug, "debug", false, "run with support for debuggers")
	flag.Parse()

	if err := providerserver.Serve(context.Background(), provider.New(version), providerserver.ServeOpts{
		Address: Address,
		Debug:   debug,
	}); err != nil {
		log.Fatal(err)
	}
}
