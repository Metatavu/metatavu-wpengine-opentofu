package provider

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"

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

// resolveInstall turns an install's ID or name into its ID.
func resolveInstall(ctx context.Context, c *client.Client, ref string) (string, error) {
	if uuidPattern.MatchString(ref) {
		return ref, nil
	}
	installs, err := c.ListInstalls(ctx)
	if err != nil {
		return "", err
	}
	for _, install := range installs {
		if install.Name == ref {
			return install.ID, nil
		}
	}
	return "", fmt.Errorf("the API user sees no install named %s", ref)
}

// splitImportID splits "<first>/<rest>".
func splitImportID(id, format string, diags *diag.Diagnostics) (string, string, bool) {
	first, rest, ok := strings.Cut(id, "/")
	if !ok || first == "" || rest == "" {
		diags.AddError("Invalid import ID", "Import with "+format+", not "+id+".")
		return "", "", false
	}
	return first, rest, true
}

func stringSet(values []string) types.Set {
	set, _ := types.SetValueFrom(context.Background(), types.StringType, append([]string{}, values...))
	return set
}

func setStrings(ctx context.Context, value types.Set, diags *diag.Diagnostics) []string {
	out := []string{}
	if value.IsNull() || value.IsUnknown() {
		return out
	}
	diags.Append(value.ElementsAs(ctx, &out, false)...)
	sort.Strings(out)
	return out
}
