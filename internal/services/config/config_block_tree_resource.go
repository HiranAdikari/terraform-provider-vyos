package config

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework-validators/listvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-log/tflog"

	"github.com/hiranadikari/terraform-provider-vyos/vyos"
)

// Ensure the resource satisfies the framework interface.
var _ resource.Resource = &ConfigBlockTreeResource{}

// NewConfigBlockTreeResource is the factory function registered with the provider.
func NewConfigBlockTreeResource() resource.Resource {
	return &ConfigBlockTreeResource{}
}

// ConfigBlockTreeResource manages an arbitrary VyOS config sub-tree.
type ConfigBlockTreeResource struct {
	client *vyos.Client
}

// configBlockTreeModel is the Terraform state/plan model.
type configBlockTreeModel struct {
	ID      types.String `tfsdk:"id"`
	Path    types.List   `tfsdk:"path"`
	Section types.String `tfsdk:"section"`
}

// ─── resource.Resource interface ─────────────────────────────────────────────

func (r *ConfigBlockTreeResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_config_block_tree"
}

func (r *ConfigBlockTreeResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: `Manages an arbitrary VyOS configuration sub-tree via the ` + "`/configure-section`" + ` endpoint.

The ` + "`section`" + ` attribute is a JSON-encoded map that is written wholesale under the specified ` + "`path`" + `.
Use ` + "`jsonencode()`" + ` in your Terraform configuration to construct the value.

**Drift detection:** This resource checks only whether the path *exists* on read.
If manual CLI edits are made, run ` + "`terraform apply`" + ` to restore Terraform-managed state.

**Example:**
` + "```hcl" + `
resource "vyos_config_block_tree" "tenant_vif" {
  path = ["interfaces", "ethernet", "eth1", "vif", "1000"]
  section = jsonencode({
    address     = "10.0.0.1/23"
    description = "tenant-a"
  })
}
` + "```",

		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Resource identifier — the config path joined by `/`.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"path": schema.ListAttribute{
				Required:            true,
				ElementType:         types.StringType,
				MarkdownDescription: "VyOS configuration path elements (e.g. `[\"interfaces\", \"ethernet\", \"eth1\", \"vif\", \"1000\"]`).",
				Validators: []validator.List{
					listvalidator.SizeAtLeast(1),
				},
			},
			"section": schema.StringAttribute{
				Required: true,
				MarkdownDescription: "JSON-encoded configuration sub-tree to write under `path`. " +
					"Use `jsonencode({...})` to construct this value.",
				Validators: []validator.String{
					stringvalidator.LengthAtLeast(2),
					validJSON{},
				},
				PlanModifiers: []planmodifier.String{
					normalizeJSONPlanModifier{},
				},
			},
		},
	}
}

func (r *ConfigBlockTreeResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	client, ok := req.ProviderData.(*vyos.Client)
	if !ok {
		resp.Diagnostics.AddError(
			"Unexpected provider data type",
			fmt.Sprintf("Expected *vyos.Client, got %T", req.ProviderData),
		)
		return
	}
	r.client = client
}

func (r *ConfigBlockTreeResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan configBlockTreeModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	path, section, d := extractModel(ctx, plan)
	resp.Diagnostics.Append(d...)
	if resp.Diagnostics.HasError() {
		return
	}

	tflog.Debug(ctx, "vyos_config_block_tree: creating", map[string]any{"path": path})

	if err := r.client.ConfigureSection(ctx, "set", path, section); err != nil {
		resp.Diagnostics.AddError("Error creating VyOS config block", err.Error())
		return
	}
	if err := r.client.SaveConfig(ctx); err != nil {
		resp.Diagnostics.AddError("Error saving VyOS config", err.Error())
		return
	}

	plan.ID = types.StringValue(strings.Join(path, "/"))
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *ConfigBlockTreeResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state configBlockTreeModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	path, _, d := extractModel(ctx, state)
	resp.Diagnostics.Append(d...)
	if resp.Diagnostics.HasError() {
		return
	}

	tflog.Debug(ctx, "vyos_config_block_tree: reading", map[string]any{"path": path})

	exists, err := r.client.Exists(ctx, path)
	if err != nil {
		resp.Diagnostics.AddError("Error reading VyOS config block", err.Error())
		return
	}
	if !exists {
		tflog.Debug(ctx, "vyos_config_block_tree: path not found, removing from state", map[string]any{"path": path})
		resp.State.RemoveResource(ctx)
		return
	}

	// Path exists — preserve current state (no leaf-level reconciliation).
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *ConfigBlockTreeResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan configBlockTreeModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	path, section, d := extractModel(ctx, plan)
	resp.Diagnostics.Append(d...)
	if resp.Diagnostics.HasError() {
		return
	}

	tflog.Debug(ctx, "vyos_config_block_tree: updating (delete + re-set)", map[string]any{"path": path})

	// Delete the existing sub-tree first so removed keys are cleaned up.
	if err := r.client.Configure(ctx, "delete", path, ""); err != nil {
		resp.Diagnostics.AddError("Error deleting existing VyOS config block during update", err.Error())
		return
	}
	if err := r.client.ConfigureSection(ctx, "set", path, section); err != nil {
		resp.Diagnostics.AddError("Error setting VyOS config block during update", err.Error())
		return
	}
	if err := r.client.SaveConfig(ctx); err != nil {
		resp.Diagnostics.AddError("Error saving VyOS config", err.Error())
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *ConfigBlockTreeResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state configBlockTreeModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	path, _, d := extractModel(ctx, state)
	resp.Diagnostics.Append(d...)
	if resp.Diagnostics.HasError() {
		return
	}

	tflog.Debug(ctx, "vyos_config_block_tree: deleting", map[string]any{"path": path})

	if err := r.client.Configure(ctx, "delete", path, ""); err != nil {
		resp.Diagnostics.AddError("Error deleting VyOS config block", err.Error())
		return
	}
	if err := r.client.SaveConfig(ctx); err != nil {
		resp.Diagnostics.AddError("Error saving VyOS config after delete", err.Error())
		return
	}
}

// ─── helpers ─────────────────────────────────────────────────────────────────

// extractModel converts the Terraform model into native Go types.
func extractModel(ctx context.Context, m configBlockTreeModel) (path []string, section map[string]any, diags diag.Diagnostics) {
	var pathElems []types.String
	diags.Append(m.Path.ElementsAs(ctx, &pathElems, false)...)
	if diags.HasError() {
		return
	}
	for _, el := range pathElems {
		path = append(path, el.ValueString())
	}

	if err := json.Unmarshal([]byte(m.Section.ValueString()), &section); err != nil {
		diags.AddError("Invalid section JSON", err.Error())
	}
	return
}

// ─── plan modifier: normalize JSON ───────────────────────────────────────────

// normalizeJSONPlanModifier normalises the JSON section value so that
// semantically equivalent JSON strings (different whitespace / key order)
// compare equal and do not produce spurious plan diffs.
type normalizeJSONPlanModifier struct{}

func (normalizeJSONPlanModifier) Description(_ context.Context) string {
	return "Normalizes JSON section for consistent state comparison."
}

func (normalizeJSONPlanModifier) MarkdownDescription(_ context.Context) string {
	return "Normalizes JSON section for consistent state comparison."
}

func (m normalizeJSONPlanModifier) PlanModifyString(_ context.Context, req planmodifier.StringRequest, resp *planmodifier.StringResponse) {
	if req.PlanValue.IsNull() || req.PlanValue.IsUnknown() {
		return
	}
	normalized, err := normalizeJSON(req.PlanValue.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Invalid JSON in section", err.Error())
		return
	}
	resp.PlanValue = types.StringValue(normalized)
}

// normalizeJSON round-trips through JSON to produce canonical compact form.
func normalizeJSON(s string) (string, error) {
	var v any
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		return "", fmt.Errorf("invalid JSON: %w", err)
	}
	out, err := json.Marshal(v)
	if err != nil {
		return "", fmt.Errorf("re-encode JSON: %w", err)
	}
	return string(out), nil
}

// ─── validator: valid JSON ────────────────────────────────────────────────────

type validJSON struct{}

func (validJSON) Description(_ context.Context) string        { return "Must be valid JSON." }
func (validJSON) MarkdownDescription(_ context.Context) string { return "Must be valid JSON." }

func (validJSON) ValidateString(_ context.Context, req validator.StringRequest, resp *validator.StringResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}
	var v any
	if err := json.Unmarshal([]byte(req.ConfigValue.ValueString()), &v); err != nil {
		resp.Diagnostics.AddAttributeError(
			req.Path,
			"Invalid JSON",
			fmt.Sprintf("section must be valid JSON: %s", err),
		)
	}
}
