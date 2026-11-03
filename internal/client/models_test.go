package client

import (
	"reflect"
	"strings"
	"testing"

	"github.com/Metatavu/metatavu-wpengine-opentofu/internal/openapi"
)

// Every field the provider reads is in its response schema, and every field
// it sends is in its operation's request body.
func TestModelsMatchTheOpenAPIDocument(t *testing.T) {
	responses := map[string]any{
		"Site":           Site{},
		"Installation":   Install{},
		"Domain":         Domain{},
		"CacheExclusion": CacheExclusion{},
		"AccountUser":    AccountUser{},
		"SshKey":         SSHKey{},
	}
	for schema, model := range responses {
		for _, field := range jsonFields(reflect.TypeOf(model), "") {
			if !openapi.HasSchemaField(schema, field) {
				t.Errorf("%s.%s isn't in the OpenAPI document (version %s)", schema, field, openapi.Version)
			}
		}
	}
	requests := map[string]any{
		"createSite":            SiteCreate{},
		"updateSite":            SiteUpdate{},
		"createInstall":         InstallCreate{},
		"updateInstall":         InstallUpdate{},
		"createDomain":          DomainCreate{},
		"updateDomain":          DomainUpdate{},
		"updateCacheExclusions": CacheExclusionsUpdate{},
		"createAccountUser":     AccountUserCreate{},
		"updateAccountUser":     AccountUserUpdate{},
		"createSshKey":          SSHKeyCreate{},
	}
	for operation, model := range requests {
		for _, field := range jsonFields(reflect.TypeOf(model), "") {
			if !openapi.HasRequestField(operation, field) {
				t.Errorf("%s's request has no %s (version %s)", operation, field, openapi.Version)
			}
		}
	}
}

func jsonFields(t reflect.Type, prefix string) []string {
	for t.Kind() == reflect.Pointer || t.Kind() == reflect.Slice {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		return nil
	}
	var out []string
	for i := 0; i < t.NumField(); i++ {
		name, _, _ := strings.Cut(t.Field(i).Tag.Get("json"), ",")
		if name == "" || name == "-" {
			continue
		}
		out = append(out, prefix+name)
		out = append(out, jsonFields(t.Field(i).Type, prefix+name+".")...)
	}
	return out
}
