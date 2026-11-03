package provider

import (
	"context"
	"fmt"
	"regexp"

	"github.com/Metatavu/metatavu-wpengine-opentofu/internal/client"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// providerData is what the provider hands its resources.
type providerData struct {
	// platform is the WP Engine API client; nil without credentials.
	platform *client.Client
}

var uuidPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// configuredPlatform is the WP Engine API client the provider configured, for
// Configure. It's nil when the provider wasn't configured or has no
// credentials; requirePlatform reports that when a resource is used.
func configuredPlatform(data any, diags *diag.Diagnostics) *client.Client {
	if data == nil {
		return nil
	}
	d, ok := data.(*providerData)
	if !ok {
		diags.AddError("Unexpected provider data", fmt.Sprintf("Expected *providerData, got %T", data))
		return nil
	}
	return d.platform
}

// requirePlatform refuses to run a platform resource without the WP Engine
// API's credentials.
func requirePlatform(c *client.Client, diags *diag.Diagnostics) bool {
	if c == nil {
		diags.AddError("No WP Engine API credentials",
			"wpengine_* platform resources need the WP Engine API: set api_username and api_password, or WPENGINE_API_USERNAME and WPENGINE_API_PASSWORD. "+
				"They're used only from a laptop, in a project's bootstrap/.")
		return false
	}
	return true
}

func stringValue(value *string) types.String {
	if value == nil {
		return types.StringNull()
	}
	return types.StringValue(*value)
}

func stringPointer(value types.String) *string {
	if value.IsNull() || value.IsUnknown() {
		return nil
	}
	s := value.ValueString()
	return &s
}

func stringList(values []string) types.List {
	list, _ := types.ListValueFrom(context.Background(), types.StringType, append([]string{}, values...))
	return list
}
