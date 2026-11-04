package provider

import (
	"context"
	"fmt"

	"github.com/Metatavu/metatavu-wpengine-opentofu/internal/wordpress"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

// optionJSON is an option value as JSON, equal to another when both are the
// same once normalised to the shape WordPress stores (provider D16): key
// order, whitespace, 5 and "5", and {} and [] don't show as changes.
type optionJSONType struct {
	basetypes.StringType
}

var (
	_ basetypes.StringTypable                    = optionJSONType{}
	_ basetypes.StringValuableWithSemanticEquals = optionJSON{}
)

func (t optionJSONType) Equal(o attr.Type) bool {
	_, ok := o.(optionJSONType)
	return ok
}

func (t optionJSONType) String() string {
	return "optionJSONType"
}

func (t optionJSONType) ValueFromString(_ context.Context, in basetypes.StringValue) (basetypes.StringValuable, diag.Diagnostics) {
	return optionJSON{StringValue: in}, nil
}

func (t optionJSONType) ValueFromTerraform(ctx context.Context, in tftypes.Value) (attr.Value, error) {
	value, err := t.StringType.ValueFromTerraform(ctx, in)
	if err != nil {
		return nil, err
	}
	stringValue, ok := value.(basetypes.StringValue)
	if !ok {
		return nil, fmt.Errorf("unexpected value type %T", value)
	}
	return optionJSON{StringValue: stringValue}, nil
}

func (t optionJSONType) ValueType(context.Context) attr.Value {
	return optionJSON{}
}

func (t optionJSONType) Validate(_ context.Context, in tftypes.Value, at path.Path) diag.Diagnostics {
	var diags diag.Diagnostics
	if !in.IsKnown() || in.IsNull() {
		return diags
	}
	var raw string
	if err := in.As(&raw); err != nil {
		diags.AddAttributeError(at, "Invalid value", err.Error())
		return diags
	}
	if _, err := wordpress.Normalise(raw); err != nil {
		diags.AddAttributeError(at, "Invalid JSON", "Give the value as JSON, for example with jsonencode(): "+err.Error())
	}
	return diags
}

type optionJSON struct {
	basetypes.StringValue
}

func (v optionJSON) Equal(o attr.Value) bool {
	other, ok := o.(optionJSON)
	return ok && v.StringValue.Equal(other.StringValue)
}

func (v optionJSON) Type(context.Context) attr.Type {
	return optionJSONType{}
}

func (v optionJSON) StringSemanticEquals(_ context.Context, newValue basetypes.StringValuable) (bool, diag.Diagnostics) {
	other, ok := newValue.(optionJSON)
	if !ok {
		return false, nil
	}
	return wordpress.Equal(v.ValueString(), other.ValueString()), nil
}

func optionJSONValue(value string) optionJSON {
	return optionJSON{StringValue: basetypes.NewStringValue(value)}
}

func optionJSONNull() optionJSON {
	return optionJSON{StringValue: basetypes.NewStringNull()}
}
