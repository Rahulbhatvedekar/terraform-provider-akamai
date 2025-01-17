package property

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/apex/log"
	"github.com/hashicorp/go-cty/cty"
	"github.com/hashicorp/terraform-plugin-sdk/v2/diag"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/customdiff"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"

	"github.com/akamai/AkamaiOPEN-edgegrid-golang/v7/pkg/papi"
	"github.com/akamai/AkamaiOPEN-edgegrid-golang/v7/pkg/session"
	"github.com/akamai/terraform-provider-akamai/v5/pkg/common/tf"
	"github.com/akamai/terraform-provider-akamai/v5/pkg/meta"
	"github.com/akamai/terraform-provider-akamai/v5/pkg/tools"
)

func resourceProperty() *schema.Resource {
	papiError := func() *schema.Resource {
		return &schema.Resource{Schema: map[string]*schema.Schema{
			"type":           {Type: schema.TypeString, Optional: true},
			"title":          {Type: schema.TypeString, Optional: true},
			"detail":         {Type: schema.TypeString, Optional: true},
			"instance":       {Type: schema.TypeString, Optional: true},
			"behavior_name":  {Type: schema.TypeString, Optional: true},
			"error_location": {Type: schema.TypeString, Optional: true},
			"status_code":    {Type: schema.TypeInt, Optional: true},
		}}
	}

	hashHostname := func(v interface{}) int {
		m, ok := v.(map[string]interface{})
		if !ok {
			return 0
		}
		cnameFrom, ok := m["cname_from"]
		if !ok {
			return 0
		}
		cnameTo, ok := m["cname_to"]
		if !ok {
			return 0
		}
		certProvisioningType, ok := m["cert_provisioning_type"]
		if !ok {
			return 0
		}
		return schema.HashString(fmt.Sprintf("%s.%s.%s", cnameFrom, cnameTo, certProvisioningType))
	}

	validateRules := func(val interface{}, _ cty.Path) diag.Diagnostics {
		if len(val.(string)) == 0 {
			return nil
		}

		var target map[string]interface{}
		if err := json.Unmarshal([]byte(val.(string)), &target); err != nil {
			return diag.Errorf("rules are not valid JSON")
		}
		return nil
	}

	return &schema.Resource{
		CreateContext: resourcePropertyCreate,
		ReadContext:   resourcePropertyRead,
		UpdateContext: resourcePropertyUpdate,
		DeleteContext: resourcePropertyDelete,
		CustomizeDiff: customdiff.All(
			rulesCustomDiff,
			hostNamesCustomDiff,
			setPropertyVersionsComputedOnRulesChange,
		),
		Importer: &schema.ResourceImporter{
			StateContext: resourcePropertyImport,
		},
		StateUpgraders: []schema.StateUpgrader{{
			Version: 0,
			Type:    resourcePropertyV0().CoreConfigSchema().ImpliedType(),
			Upgrade: upgradePropV0,
		}},
		SchemaVersion: 1,
		Schema: map[string]*schema.Schema{
			"name": {
				Type:             schema.TypeString,
				Required:         true,
				ForceNew:         true,
				ValidateDiagFunc: validatePropertyName,
				Description:      "Name to give to the Property (must be unique)",
			},
			"group_id": {
				Type:        schema.TypeString,
				Required:    true,
				StateFunc:   addPrefixToState("grp_"),
				Description: "Group ID to be assigned to the Property",
			},
			"contract_id": {
				Type:        schema.TypeString,
				Required:    true,
				StateFunc:   addPrefixToState("ctr_"),
				Description: "Contract ID to be assigned to the Property",
			},
			"product_id": {
				Type:        schema.TypeString,
				Required:    true,
				Description: "Product ID to be assigned to the Property",
				StateFunc:   addPrefixToState("prd_"),
			},
			"rule_format": {
				Type:             schema.TypeString,
				Optional:         true,
				Computed:         true,
				Description:      "Specify the rule format version (defaults to latest version available when created)",
				ValidateDiagFunc: tf.ValidateRuleFormatAcceptLatest,
			},
			"rules": {
				Type:             schema.TypeString,
				Optional:         true,
				Computed:         true,
				Description:      "Property Rules as JSON",
				ValidateDiagFunc: validateRules,
				DiffSuppressFunc: diffSuppressRules,
				StateFunc:        rulesStateFunc,
			},
			"hostnames": {
				Type:     schema.TypeSet,
				Optional: true,
				Set:      hashHostname,
				Elem: &schema.Resource{
					Schema: map[string]*schema.Schema{
						"cname_from": {
							Type:     schema.TypeString,
							Required: true,
							ValidateDiagFunc: func(i interface{}, path cty.Path) diag.Diagnostics {
								if len(i.(string)) == 0 {
									return diag.Errorf("'cname_from' cannot be empty when hostnames block is defined - See new hostnames schema")
								}
								return nil
							},
						},
						"cname_to": {
							Type:     schema.TypeString,
							Required: true,
							ValidateDiagFunc: func(i interface{}, path cty.Path) diag.Diagnostics {
								if len(i.(string)) == 0 {
									return diag.Errorf("'cname_to' cannot be empty when hostnames block is defined - See new hostnames schema")
								}
								return nil
							},
						},
						"cert_provisioning_type": {
							Type:     schema.TypeString,
							Required: true,
							ValidateDiagFunc: func(i interface{}, path cty.Path) diag.Diagnostics {
								if len(i.(string)) == 0 {
									return diag.Errorf("'cert_provisioning_type' cannot be empty when hostnames block is defined - See new hostnames schema")
								}
								return nil
							},
						},
						"cname_type": {
							Type:     schema.TypeString,
							Optional: true,
							Computed: true,
						},
						"edge_hostname_id": {
							Type:     schema.TypeString,
							Computed: true,
						},
						"cert_status": {
							Type:     schema.TypeList,
							Optional: true,
							Computed: true,
							Elem:     certStatus,
						},
					},
				},
			},
			"latest_version": {
				Type:        schema.TypeInt,
				Computed:    true,
				Description: "Property's current latest version number",
			},
			"staging_version": {
				Type:        schema.TypeInt,
				Computed:    true,
				Description: "Property's version currently activated in staging (zero when not active in staging)",
			},
			"production_version": {
				Type:        schema.TypeInt,
				Computed:    true,
				Description: "Property's version currently activated in production (zero when not active in production)",
			},
			"read_version": {
				Type:        schema.TypeInt,
				Computed:    true,
				Description: "Required property's version to be read",
			},
			"rule_errors": {
				Type:     schema.TypeList,
				Computed: true,
				Elem:     papiError(),
			},
		},
	}
}

var rulesStateFunc = func(v interface{}) string {
	var js string
	if json.Unmarshal([]byte(v.(string)), &js) == nil {
		return compactJSON([]byte(v.(string)))
	}
	return v.(string)
}

// isValidPropertyName is a function that validates if given string contains only letters, numbers, and these characters: . _ -
var isValidPropertyName = regexp.MustCompile(`^[A-Za-z0-9.\-_]+$`).MatchString

// validatePropertyName validates if name property contains valid characters
func validatePropertyName(v interface{}, _ cty.Path) diag.Diagnostics {
	name := v.(string)
	maxPropertyNameLength := 85

	if len(name) > maxPropertyNameLength {
		return diag.Errorf("a name must be shorter than %d characters", maxPropertyNameLength+1)
	}
	if !isValidPropertyName(name) {
		return diag.Errorf("a name must only contain letters, numbers, and these characters: . _ -")
	}
	return nil
}

// rulesCustomDiff compares Rules.Criteria and Rules.Children fields from terraform state and from a new configuration.
// If some of these fields are empty lists in the new configuration and are nil in the terraform state, then this function
// returns no difference for these fields
func rulesCustomDiff(_ context.Context, diff *schema.ResourceDiff, _ interface{}) error {
	o, n := diff.GetChange("rules")

	oldValue := o.(string)
	newValue := n.(string)

	var oldRulesUpdate, newRulesUpdate papi.RulesUpdate

	if diff.Id() == "" && newValue != "" {
		rules, err := unifyRulesDiff(newValue)
		if err != nil {
			return err
		}
		if err = diff.SetNew("rules", rules); err != nil {
			return fmt.Errorf("cannot set a new diff value for 'rules' %s", err)
		}
		return nil
	}

	if oldValue == "" || newValue == "" {
		return nil
	}

	err := json.Unmarshal([]byte(oldValue), &oldRulesUpdate)
	if err != nil {
		return fmt.Errorf("cannot parse rules JSON from state: %s", err)
	}

	err = json.Unmarshal([]byte(newValue), &newRulesUpdate)
	if err != nil {
		return fmt.Errorf("cannot parse rules JSON from config: %s", err)
	}

	normalizeFields(&oldRulesUpdate, &newRulesUpdate)
	rules, err := json.Marshal(newRulesUpdate)
	if err != nil {
		return fmt.Errorf("cannot encode rules JSON %s", err)
	}
	if ruleTreesEqual(&oldRulesUpdate, &newRulesUpdate) {
		return nil
	}
	if err = diff.SetNew("rules", string(rules)); err != nil {
		return fmt.Errorf("cannot set a new diff value for 'rules' %s", err)
	}
	return nil
}

// unifyRulesDiff is invoked on first planning for property creation
// Its main purpose is to unify the rules JSON with what we expect will be created by PAPI
// It is used in order to prevent diffs on output on subsequent terraform applies
func unifyRulesDiff(newValue string) (string, error) {
	var newRulesUpdate papi.RulesUpdate
	err := json.Unmarshal([]byte(newValue), &newRulesUpdate)
	if err != nil {
		return "", fmt.Errorf("cannot parse rules JSON from config: %s", err)
	}
	removeNilOptions(&newRulesUpdate.Rules)
	rulesBytes, err := json.Marshal(newRulesUpdate)
	if err != nil {
		return "", err
	}
	return string(rulesBytes), nil
}

func normalizeFields(oldRules, newRules *papi.RulesUpdate) {
	if oldRules.Rules.Children == nil && len(newRules.Rules.Children) == 0 {
		newRules.Rules.Children = oldRules.Rules.Children
	}
	if oldRules.Rules.Criteria == nil && len(newRules.Rules.Criteria) == 0 {
		newRules.Rules.Criteria = oldRules.Rules.Criteria
	}
}

func hostNamesCustomDiff(_ context.Context, d *schema.ResourceDiff, m interface{}) error {
	meta := meta.Must(m)
	logger := meta.Log("PAPI", "hostNamesCustomDiff")

	o, n := d.GetChange("hostnames")
	oldVal, ok := o.(*schema.Set)
	if !ok {
		logger.Errorf("error parsing local state for old value %s", oldVal)
		return fmt.Errorf("cannot parse hostnames state properly %v", o)
	}

	newVal, ok := n.(*schema.Set)
	if !ok {
		logger.Errorf("error parsing local state for new value %s", newVal)
		return fmt.Errorf("cannot parse hostnames state properly %v", n)
	}
	// PAPI doesn't allow hostnames to become empty if they already exist on server
	// TODO Do we add support for hostnames patch operation to enable this?
	if len(oldVal.List()) > 0 && len(newVal.List()) == 0 {
		logger.Errorf("Hostnames exist on server and cannot be updated to empty for %d", d.Id())
		return fmt.Errorf("hostnames exist on server and cannot be updated to empty for property with id '%s'. Provide at least one hostname to update existing list of hostnames associated to this property", d.Id())
	}
	return nil
}

// setPropertyVersionsComputedOnRulesChange is a schema.CustomizeDiffFunc for akamai_property resource,
// which sets latest_version, staging_version and production_version fields as computed
// if a new version of the property is expected to be created.
func setPropertyVersionsComputedOnRulesChange(_ context.Context, rd *schema.ResourceDiff, _ interface{}) error {
	oldHostnames, newHostnames := rd.GetChange("hostnames")
	hostnamesEqual := oldHostnames.(*schema.Set).HashEqual(newHostnames.(*schema.Set))
	ruleFormatChanged := rd.HasChange("rule_format")

	oldRules, newRules := rd.GetChange("rules")
	rulesEqual, err := rulesJSONEqual(oldRules.(string), newRules.(string))
	if err != nil {
		return err
	}

	if !ruleFormatChanged && hostnamesEqual && rulesEqual {
		return nil
	}

	for _, key := range []string{"latest_version", "staging_version", "production_version"} {
		if err := rd.SetNewComputed(key); err != nil {
			return fmt.Errorf("%w: %s", tf.ErrValueSet, err.Error())
		}
	}

	return nil
}

func resourcePropertyCreate(ctx context.Context, d *schema.ResourceData, m interface{}) diag.Diagnostics {
	meta := meta.Must(m)
	logger := meta.Log("PAPI", "resourcePropertyCreate")
	client := Client(meta)
	ctx = log.NewContext(ctx, logger)

	// Schema guarantees these types
	propertyName := d.Get("name").(string)

	groupID, err := tf.GetStringValue("group_id", d)
	if err != nil {
		return diag.FromErr(err)
	}
	groupID = tools.AddPrefix(groupID, "grp_")

	contractID, err := tf.GetStringValue("contract_id", d)
	if err != nil {
		return diag.FromErr(err)
	}
	contractID = tools.AddPrefix(contractID, "ctr_")

	productID, err := tf.GetStringValue("product_id", d)
	if err != nil {
		return diag.FromErr(err)
	}
	productID = tools.AddPrefix(productID, "prd_")

	ruleFormat := d.Get("rule_format").(string)

	rulesJSON := []byte(d.Get("rules").(string))

	propertyID, err := createProperty(ctx, client, propertyName, groupID, contractID, productID, ruleFormat)
	if err != nil {
		if strings.Contains(err.Error(), "\"statusCode\": 404") {
			// find out what is missing from the request
			if _, err = getGroup(ctx, meta, groupID); err != nil {
				if errors.Is(err, ErrGroupNotFound) {
					return diag.Errorf("%v: %s", ErrGroupNotFound, groupID)
				}
				return diag.FromErr(err)
			}
			if _, err = getContract(ctx, meta, contractID); err != nil {
				if errors.Is(err, ErrContractNotFound) {
					return diag.Errorf("%v: %s", ErrContractNotFound, contractID)
				}
				return diag.FromErr(err)
			}
			if _, err = getProduct(ctx, meta, productID, contractID); err != nil {
				if errors.Is(err, ErrProductNotFound) {
					return diag.Errorf("%v: %s", ErrProductNotFound, productID)
				}
				return diag.FromErr(err)
			}
			return diag.FromErr(err)
		}
		return diag.FromErr(err)
	}

	// Save minimum state BEFORE moving on
	d.SetId(propertyID)
	attrs := map[string]interface{}{
		"group_id":    groupID,
		"contract_id": contractID,
		"product_id":  productID,
	}
	if err := rdSetAttrs(ctx, d, attrs); err != nil {
		return diag.FromErr(err)
	}

	property := papi.Property{
		PropertyName:  propertyName,
		PropertyID:    propertyID,
		ContractID:    contractID,
		GroupID:       groupID,
		ProductID:     productID,
		LatestVersion: 1,
	}
	hostnameVal, err := tf.GetSetValue("hostnames", d)
	if err == nil {
		hostnames := mapToHostnames(hostnameVal.List())
		if len(hostnames) > 0 {
			if err := updatePropertyHostnames(ctx, client, property, hostnames); err != nil {
				return diag.FromErr(err)
			}
		}
	} else {
		logger.Warnf("hostnames not set in ResourceData: %s", err.Error())
	}

	if len(rulesJSON) > 0 {
		var rules papi.RulesUpdate
		if err := json.Unmarshal(rulesJSON, &rules); err != nil {
			logger.WithError(err).Error("failed to unmarshal property rules")
			return diag.Errorf("rules are not valid JSON: %s", err)
		}

		ctx := ctx
		if ruleFormat != "" {
			h := http.Header{
				"Content-Type": []string{fmt.Sprintf("application/vnd.akamai.papirules.%s+json", ruleFormat)},
			}

			ctx = session.ContextWithOptions(ctx, session.WithContextHeaders(h))
		}

		if err := updatePropertyRules(ctx, client, property, rules); err != nil {
			d.Partial(true)
			return diag.FromErr(err)
		}
	}

	return resourcePropertyRead(ctx, d, m)
}

func resourcePropertyRead(ctx context.Context, d *schema.ResourceData, m interface{}) diag.Diagnostics {
	ctx = log.NewContext(ctx, meta.Must(m).Log("PAPI", "resourcePropertyRead"))
	logger := log.FromContext(ctx)
	client := Client(meta.Must(m))

	// Schema guarantees group_id, and contract_id are strings
	propertyID := d.Id()
	contractID := tools.AddPrefix(d.Get("contract_id").(string), "ctr_")
	groupID := tools.AddPrefix(d.Get("group_id").(string), "grp_")
	readVersionID := d.Get("read_version").(int)

	var property *papi.Property
	var err error
	var v int
	if readVersionID == 0 {
		property, err = fetchLatestProperty(ctx, client, propertyID, groupID, contractID)
	} else {
		property, v, err = fetchProperty(ctx, client, propertyID, groupID, contractID, strconv.Itoa(readVersionID))
	}
	if err != nil {
		return diag.FromErr(err)
	}
	if v == 0 {
		// use latest version unless "read_version" != 0
		v = property.LatestVersion
	}

	var stagingVersion int
	if property.StagingVersion != nil {
		stagingVersion = *property.StagingVersion
	}

	var productionVersion int
	if property.ProductionVersion != nil {
		productionVersion = *property.ProductionVersion
	}

	// TODO: Load hostnames asynchronously
	hostnames, err := fetchPropertyVersionHostnames(ctx, client, *property, v)
	if err != nil {
		return diag.FromErr(err)
	}

	// TODO: Load rules asynchronously
	rules, ruleFormat, ruleErrors, ruleWarnings, err := fetchPropertyVersionRules(ctx, client, *property, v)
	if err != nil {
		return diag.FromErr(err)
	}
	if len(ruleErrors) > 0 {
		if err := d.Set("rule_errors", papiErrorsToList(ruleErrors)); err != nil {
			return diag.FromErr(fmt.Errorf("%w: %s", tf.ErrValueSet, err.Error()))
		}
		msg, err := json.MarshalIndent(papiErrorsToList(ruleErrors), "", "\t")
		if err != nil {
			return diag.FromErr(fmt.Errorf("error marshaling API error: %s", err))
		}
		logger.Errorf("property has rule errors %s", msg)
	}
	if len(ruleWarnings) > 0 {
		msg, err := json.MarshalIndent(papiErrorsToList(ruleWarnings), "", "\t")
		if err != nil {
			return diag.FromErr(fmt.Errorf("error marshaling API warnings: %s", err))
		}
		logger.Warnf("property has rule warnings %s", msg)
	}

	rulesJSON, err := json.Marshal(rules)
	if err != nil {
		logger.WithError(err).Error("could not render rules as JSON")
		return diag.Errorf("received rules that could not be rendered to JSON: %s", err)
	}
	res, err := fetchPropertyVersion(ctx, client, propertyID, groupID, contractID, v)
	if err != nil {
		return diag.FromErr(err)
	}
	property.ProductID = res.Version.ProductID

	attrs := map[string]interface{}{
		"name":               property.PropertyName,
		"group_id":           property.GroupID,
		"contract_id":        property.ContractID,
		"latest_version":     property.LatestVersion,
		"staging_version":    stagingVersion,
		"production_version": productionVersion,
		"hostnames":          flattenHostnames(hostnames),
		"rules":              string(rulesJSON),
		"rule_format":        ruleFormat,
		"rule_errors":        papiErrorsToList(ruleErrors),
		"read_version":       readVersionID,
	}
	if property.ProductID != "" {
		attrs["product_id"] = property.ProductID
	}
	if err := rdSetAttrs(ctx, d, attrs); err != nil {
		return diag.FromErr(err)
	}

	return nil
}

func resourcePropertyUpdate(ctx context.Context, d *schema.ResourceData, m interface{}) diag.Diagnostics {
	ctx = log.NewContext(ctx, meta.Must(m).Log("PAPI", "resourcePropertyUpdate"))
	logger := log.FromContext(ctx)
	client := Client(meta.Must(m))

	diags := diag.Diagnostics{}

	immutable := []string{
		"group_id",
		"contract_id",
		"product_id",
	}
	for _, attr := range immutable {
		if d.HasChange(attr) {
			err := fmt.Errorf(`property attribute %q cannot be changed after creation (immutable)`, attr)
			logger.WithError(err).Error("could not update property")
			diags = append(diags, diag.FromErr(err)...)
		}
	}
	if diags.HasError() {
		d.Partial(true)
		return diags
	}

	// We only update if these attributes change.
	if !d.HasChanges("hostnames", "rules", "rule_format") {
		logger.Debug("No changes to hostnames, rules, or rule_format (no update required)")
		return nil
	}

	// Schema guarantees these types
	var stagingVersion, productionVersion *int
	if v, ok := d.GetOk("staging_version"); ok && v.(int) != 0 {
		i := v.(int)
		stagingVersion = &i
	}

	if v, ok := d.GetOk("production_version"); ok && v.(int) != 0 {
		i := v.(int)
		productionVersion = &i
	}

	property := papi.Property{
		PropertyID:        d.Id(),
		PropertyName:      d.Get("name").(string),
		ContractID:        d.Get("contract_id").(string),
		GroupID:           d.Get("group_id").(string),
		ProductID:         d.Get("product_id").(string),
		LatestVersion:     d.Get("latest_version").(int),
		StagingVersion:    stagingVersion,
		ProductionVersion: productionVersion,
	}

	// Schema guarantees group_id, and contract_id are strings
	propertyID := d.Id()
	contractID := d.Get("contract_id").(string)
	groupID := d.Get("group_id").(string)

	var propertyVersion int
	if v, ok := d.GetOk("read_version"); ok && v.(int) != 0 {
		propertyVersion = v.(int)
	} else {
		propertyVersion = property.LatestVersion
	}

	resp, err := fetchPropertyVersion(ctx, client, propertyID, groupID, contractID, propertyVersion)
	if err != nil {
		d.Partial(true)
		return diag.FromErr(err)
	}

	// if read_version is not the latest version or not editable then create a new version from it before proceeding
	if (propertyVersion != property.LatestVersion) || (resp.Version.ProductionStatus != papi.VersionStatusInactive || resp.Version.StagingStatus != papi.VersionStatusInactive) {
		// The latest version has been activated on either production or staging, so we need to create a new version to apply changes on
		versionID, err := createPropertyVersion(ctx, client, property, propertyVersion)
		if err != nil {
			d.Partial(true)
			return diag.FromErr(err)
		}
		property.LatestVersion = versionID
		if err = d.Set("read_version", 0); err != nil {
			return diag.FromErr(err)
		}
	}

	// hostnames
	if d.HasChange("hostnames") {
		hostnamesVal, err := tf.GetSetValue("hostnames", d)
		if err == nil {
			hostnames := mapToHostnames(hostnamesVal.List())
			if len(hostnames) > 0 {
				if err := updatePropertyHostnames(ctx, client, property, hostnames); err != nil {
					d.Partial(true)
					return diag.FromErr(err)
				}
			}
		} else {
			logger.Warnf("hostnames not set in ResourceData: %s", err.Error())
		}
	}

	ruleFormat := d.Get("rule_format").(string)
	rulesJSON := []byte(d.Get("rules").(string))
	rulesNeedUpdate := len(rulesJSON) > 0 && d.HasChange("rules")
	formatNeedsUpdate := len(ruleFormat) > 0 && d.HasChange("rule_format")

	if err := needsUpdate(ctx, d, formatNeedsUpdate, rulesNeedUpdate, rulesJSON, ruleFormat, client, property); err != nil {
		return diag.FromErr(err)
	}

	return resourcePropertyRead(ctx, d, m)
}

func resourcePropertyDelete(ctx context.Context, d *schema.ResourceData, m interface{}) diag.Diagnostics {
	ctx = log.NewContext(ctx, meta.Must(m).Log("PAPI", "resourcePropertyDelete"))
	client := Client(meta.Must(m))

	propertyID := d.Id()
	contractID := tools.AddPrefix(d.Get("contract_id").(string), "ctr_")
	groupID := tools.AddPrefix(d.Get("group_id").(string), "grp_")

	if err := removeProperty(ctx, client, propertyID, groupID, contractID); err != nil {
		return diag.FromErr(err)
	}

	return nil
}

func resourcePropertyImport(ctx context.Context, d *schema.ResourceData, m interface{}) ([]*schema.ResourceData, error) {
	ctx = log.NewContext(ctx, meta.Must(m).Log("PAPI", "resourcePropertyImport"))

	// User-supplied import ID is a comma-separated list of propertyID[,groupID[,contractID]]
	// contractID and groupID are optional as long as the propertyID is sufficient to fetch the property
	var propertyID, groupID, contractID, version string
	parts := strings.Split(d.Id(), ",")
	switch len(parts) {
	case 4:
		version = parts[3]
		fallthrough
	case 3:
		propertyID = tools.AddPrefix(parts[0], "prp_")
		contractID = tools.AddPrefix(parts[1], "ctr_")
		groupID = tools.AddPrefix(parts[2], "grp_")
	case 2:
		version = parts[1]
		fallthrough
	case 1:
		propertyID = tools.AddPrefix(parts[0], "prp_")

	default:
		return nil, fmt.Errorf("invalid property identifier: %q", d.Id())
	}

	// Import only needs to set the resource ID and enough attributes that the read operation will function, so there's
	// no need to fetch anything if the user gave both groupID and contractID
	if groupID != "" && contractID != "" {
		attrs := map[string]interface{}{
			"group_id":    groupID,
			"contract_id": contractID,
		}

		// if we also get the optional version parameter, we need to parse it and set it in the schema
		if !isDefaultVersion(version) {
			if v, err := parseVersionNumber(version); err != nil {
				// acceptable values for version at this point: "PRODUCTION" or "STAGING" (or synonyms). Let's validate
				if _, err := NetworkAlias(version); err != nil {
					return nil, ErrPropertyVersionNotFound
				}
				// if we ran validation and we actually have a network name, we still need to fetch the desired version number
				_, attrs["read_version"], err = fetchProperty(ctx, Client(meta.Must(m)), propertyID, groupID, contractID, version)
				if err != nil {
					return nil, err
				}
			} else {
				// if the version number can be parsed as a number or ver_#, nothing else to be done
				attrs["read_version"] = v
			}
		}
		if err := rdSetAttrs(ctx, d, attrs); err != nil {
			return nil, err
		}

		d.SetId(propertyID)
		return []*schema.ResourceData{d}, nil
	}

	var err error
	var property *papi.Property
	var v int
	if !isDefaultVersion(version) {
		property, v, err = fetchProperty(ctx, Client(meta.Must(m)), propertyID, groupID, contractID, version)
	} else {
		property, err = fetchLatestProperty(ctx, Client(meta.Must(m)), propertyID, groupID, contractID)
	}
	if err != nil {
		return nil, err
	}

	attrs := map[string]interface{}{
		"group_id":     property.GroupID,
		"contract_id":  property.ContractID,
		"read_version": v,
	}
	if err := rdSetAttrs(ctx, d, attrs); err != nil {
		return nil, err
	}

	d.SetId(property.PropertyID)
	return []*schema.ResourceData{d}, nil
}

func isDefaultVersion(version string) bool {
	return version == "" || strings.ToLower(version) == "latest"
}

var versionRegexp = regexp.MustCompile(`^ver_(\d+)$`)

// parse a version number (format "ver_#" or "#") or throw an error
func parseVersionNumber(version string) (int, error) {
	v := tools.AddPrefix(version, "ver_")
	r := versionRegexp
	matches := r.FindStringSubmatch(v)
	if len(matches) < 2 {
		return 0, fmt.Errorf("invalid version number")
	}
	versionNumber, err := strconv.Atoi(matches[1])
	return versionNumber, err
}

func createProperty(ctx context.Context, client papi.PAPI, propertyName, groupID, contractID, productID, ruleFormat string) (propertyID string, err error) {
	req := papi.CreatePropertyRequest{
		ContractID: contractID,
		GroupID:    groupID,
		Property: papi.PropertyCreate{
			ProductID:    productID,
			PropertyName: propertyName,
			RuleFormat:   ruleFormat,
		},
	}

	logger := log.FromContext(ctx).WithFields(logFields(req))

	logger.Debug("creating property")
	res, err := client.CreateProperty(ctx, req)
	if err != nil {
		logger.WithError(err).Error("could not create property")
		return
	}
	propertyID = res.PropertyID

	logger.WithFields(logFields(*res)).Info("property created")
	return
}

func removeProperty(ctx context.Context, client papi.PAPI, propertyID, groupID, contractID string) error {
	req := papi.RemovePropertyRequest{
		PropertyID: propertyID,
		GroupID:    groupID,
		ContractID: contractID,
	}

	logger := log.FromContext(ctx).WithFields(logFields(req))
	logger.Debug("removing property")

	_, err := client.RemoveProperty(ctx, req)
	if err != nil {
		logger.WithError(err).Error("could not remove property")
		return err
	}

	logger.Info("property removed")

	return nil
}

func fetchLatestProperty(ctx context.Context, client papi.PAPI, propertyID, groupID, contractID string) (*papi.Property, error) {
	req := papi.GetPropertyRequest{
		PropertyID: propertyID,
		ContractID: contractID,
		GroupID:    groupID,
	}
	logger := log.FromContext(ctx).WithFields(logFields(req))
	logger.Debug("fetching property")
	res, err := client.GetProperty(ctx, req)
	if err != nil {
		logger.WithError(err).Error("could not read property")
		return nil, err
	}

	logger = logger.WithFields(logFields(*res))

	if res.Property == nil {
		err := fmt.Errorf("PAPI::GetProperty() response did not contain a property")
		logger.WithError(err).Error("could not look up property")
		return nil, err
	}

	logger.Debug("property fetched")
	return res.Property, nil
}

// fetchProperty Retrieves basic info for a Property
func fetchProperty(ctx context.Context, client papi.PAPI, propertyID, groupID, contractID, version string) (*papi.Property, int, error) {
	req := papi.GetPropertyVersionsRequest{
		PropertyID: propertyID,
		ContractID: contractID,
		GroupID:    groupID,
	}
	logger := log.FromContext(ctx).WithFields(logFields(req))
	logger.Debugf("fetching property versions")
	res, err := client.GetPropertyVersions(ctx, req)
	if err != nil {
		logger.WithError(err).Error("could not read property versions")
		return nil, 0, err
	}

	versions := res.Versions.Items
	var versionNumber int
	if network, err := NetworkAlias(version); err != nil {
		// if it is a valid version number there is nothing else to do
		n, err := parseVersionNumber(version)
		if err != nil {
			return nil, 0, ErrPropertyVersionNotFound
		}
		versionNumber = n
	} else {
		// filter production
		if network == string(papi.ActivationNetworkProduction) {
			versions, err = filterProduction(versions)
			if err != nil {
				return nil, 0, err
			}
		}

		// filter staging
		if network == string(papi.ActivationNetworkStaging) {
			versions, err = filterStaging(versions)
			if err != nil {
				return nil, 0, err
			}
		}

		versionNumber = getLatestVersionNumber(versions)
	}
	versionItem, err := getVersionItem(versions, versionNumber)
	if err != nil {
		return nil, 0, err
	}

	property := papi.Property{
		AccountID:         res.AccountID,
		ContractID:        res.ContractID,
		GroupID:           res.GroupID,
		PropertyID:        res.PropertyID,
		PropertyName:      res.PropertyName,
		LatestVersion:     getLatestVersionNumber(res.Versions.Items),
		StagingVersion:    getNetworkActiveVersionNumber(res.Versions.Items, string(papi.ActivationNetworkStaging)),
		ProductionVersion: getNetworkActiveVersionNumber(res.Versions.Items, string(papi.ActivationNetworkProduction)),
		AssetID:           res.AssetID,
		Note:              versionItem.Note,
		ProductID:         versionItem.ProductID,
		RuleFormat:        versionItem.RuleFormat,
	}

	logger.Debug("property versions fetched")

	return &property, versionNumber, nil
}

// filterStaging filters papi.PropertyVersionGetItem elements with StagingStatus == "ACTIVE"
// from the given list
func filterStaging(items []papi.PropertyVersionGetItem) ([]papi.PropertyVersionGetItem, error) {
	var output []papi.PropertyVersionGetItem
	for _, it := range items {
		if it.StagingStatus == "ACTIVE" {
			output = append(output, it)
		}
	}
	if len(output) == 0 {
		return nil, ErrPropertyVersionNotFound
	}
	return output, nil
}

// filterProduction filters papi.PropertyVersionGetItem elements with ProductionStatus == "ACTIVE"
// from the given list
func filterProduction(items []papi.PropertyVersionGetItem) ([]papi.PropertyVersionGetItem, error) {
	var output []papi.PropertyVersionGetItem
	for _, it := range items {
		if it.ProductionStatus == "ACTIVE" {
			output = append(output, it)
		}
	}
	if len(output) == 0 {
		return nil, ErrPropertyVersionNotFound
	}
	return output, nil
}

// getLatestVersionNumber returns from the given list the highest papi.PropertyVersionGetItem
// PropertyVersion from the list
func getLatestVersionNumber(items []papi.PropertyVersionGetItem) int {
	var latest int
	for _, it := range items {
		if it.PropertyVersion > latest {
			latest = it.PropertyVersion
		}
	}
	return latest
}

// getNetworkActiveVersionNumber returns from the given list the *papi.PropertyVersionGetItem
// active in the given network
func getNetworkActiveVersionNumber(items []papi.PropertyVersionGetItem, network string) *int {
	for _, it := range items {
		switch network {
		case string(papi.ActivationNetworkStaging):
			if it.StagingStatus == "ACTIVE" {
				return &it.PropertyVersion
			}
		case string(papi.ActivationNetworkProduction):
			if it.ProductionStatus == "ACTIVE" {
				return &it.PropertyVersion
			}
		}
	}
	return nil
}

func getVersionItem(items []papi.PropertyVersionGetItem, versionNumber int) (*papi.PropertyVersionGetItem, error) {
	for _, it := range items {
		if it.PropertyVersion == versionNumber {
			return &it, nil
		}
	}
	return nil, ErrPropertyVersionNotFound
}

// load status for what we currently have as a given property version.  GetLatestVersion may also work here.
func fetchPropertyVersion(ctx context.Context, client papi.PAPI, propertyID, groupID, contractID string, propertyVersion int) (*papi.GetPropertyVersionsResponse, error) {
	req := papi.GetPropertyVersionRequest{
		PropertyID:      propertyID,
		ContractID:      contractID,
		GroupID:         groupID,
		PropertyVersion: propertyVersion,
	}
	logger := log.FromContext(ctx).WithFields(logFields(req))
	logger.Debug("fetching property version")

	res, err := client.GetPropertyVersion(ctx, req)
	if err != nil {
		logger.WithError(err).Error("could not read property version")
		return nil, err
	}
	logger = logger.WithFields(logFields(*res))
	logger.Debug("property version fetched")
	return res, err
}

// Fetch hostnames for latest version of given property
func fetchPropertyVersionHostnames(ctx context.Context, client papi.PAPI, property papi.Property, version int) ([]papi.Hostname, error) {
	req := papi.GetPropertyVersionHostnamesRequest{
		PropertyID:        property.PropertyID,
		GroupID:           property.GroupID,
		ContractID:        property.ContractID,
		PropertyVersion:   version,
		IncludeCertStatus: true,
	}

	logger := log.FromContext(ctx).WithFields(logFields(req))

	logger.Debug("fetching property hostnames")
	res, err := client.GetPropertyVersionHostnames(ctx, req)
	if err != nil {
		logger.WithError(err).Error("could not fetch property hostnames")
		return nil, err
	}

	logger.WithFields(logFields(*res)).Debug("fetched property hostnames")
	return res.Hostnames.Items, nil
}

// Fetch rules for latest version of given property
func fetchPropertyVersionRules(ctx context.Context, client papi.PAPI, property papi.Property, version int) (rules papi.RulesUpdate, format string, errors, warnings []*papi.Error, err error) {
	req := papi.GetRuleTreeRequest{
		PropertyID:      property.PropertyID,
		GroupID:         property.GroupID,
		ContractID:      property.ContractID,
		PropertyVersion: version,
		ValidateRules:   true,
		ValidateMode:    papi.RuleValidateModeFull,
	}

	logger := log.FromContext(ctx).WithFields(logFields(req))

	logger.Debug("fetching property rules")
	res, err := client.GetRuleTree(ctx, req)
	if err != nil {
		logger.WithError(err).Error("could not fetch property rules")
		return
	}

	logger.WithFields(logFields(*res)).Debug("fetched property rules")
	rules = papi.RulesUpdate{
		Rules:    res.Rules,
		Comments: res.Comments,
	}
	format = res.RuleFormat
	errors = res.Errors
	warnings = res.Warnings
	return
}

// Set rules for the latest version of the given property
func updatePropertyRules(ctx context.Context, client papi.PAPI, property papi.Property, rules papi.RulesUpdate) error {
	req := papi.UpdateRulesRequest{
		PropertyID:      property.PropertyID,
		GroupID:         property.GroupID,
		ContractID:      property.ContractID,
		PropertyVersion: property.LatestVersion,
		Rules:           rules,
		ValidateRules:   true,
	}

	logger := log.FromContext(ctx).WithFields(logFields(req))

	logger.Debug("fetching property rules")
	res, err := client.UpdateRuleTree(ctx, req)
	if err != nil {
		logger.WithError(err).Error("could not update property rules")
		return err
	}

	logger.WithFields(logFields(*res)).Info("updated property rules")
	return nil
}

// Create a new property version based on the latest version of the given property
func createPropertyVersion(ctx context.Context, client papi.PAPI, property papi.Property, version int) (newVersion int, err error) {
	req := papi.CreatePropertyVersionRequest{
		PropertyID: property.PropertyID,
		ContractID: property.ContractID,
		GroupID:    property.GroupID,
		Version: papi.PropertyVersionCreate{
			CreateFromVersion: version,
		},
	}

	logger := log.FromContext(ctx).WithFields(logFields(req))

	logger.Debug(fmt.Sprintf("creating new property version from previous version %d", version))
	res, err := client.CreatePropertyVersion(ctx, req)
	if err != nil {
		logger.WithError(err).Error("could not create new property version")
		return
	}

	logger.WithFields(logFields(*res)).Info("property version created")
	newVersion = res.PropertyVersion
	return
}

// Set hostnames of the latest version of the given property
func updatePropertyHostnames(ctx context.Context, client papi.PAPI, property papi.Property, hostnames []papi.Hostname) error {
	if hostnames == nil {
		hostnames = []papi.Hostname{}
	}
	req := papi.UpdatePropertyVersionHostnamesRequest{
		PropertyID:      property.PropertyID,
		GroupID:         property.GroupID,
		ContractID:      property.ContractID,
		PropertyVersion: property.LatestVersion,
		Hostnames:       hostnames,
	}

	logger := log.FromContext(ctx).WithFields(logFields(req))

	logger.Debug("updating property hostnames")
	res, err := client.UpdatePropertyVersionHostnames(ctx, req)
	if err != nil {
		hasDefaultProvisioningType := false
		for _, h := range hostnames {
			if h.CertProvisioningType == "DEFAULT" {
				hasDefaultProvisioningType = true
				break
			}
		}

		if hasDefaultProvisioningType {
			if errors.Is(err, papi.ErrSBDNotEnabled) {
				err = fmt.Errorf("%s: not possible to use cert_provisioning_type = 'DEFAULT' as secure-by-default is not enabled in this account", papi.ErrUpdatePropertyVersionHostnames)
			}
			if errors.Is(err, papi.ErrDefaultCertLimitReached) {
				err = fmt.Errorf("%s: not possible to use cert_provisioning_type = 'DEFAULT' as the limit for DEFAULT certificates has been reached", papi.ErrUpdatePropertyVersionHostnames)
			}
		}
		logger.WithError(err).Error("could not modify the hostnames for a property version")
		return err
	}

	logger.WithFields(logFields(*res)).Info("property hostnames updated")
	return nil
}

// Convert the given map from a schema.ResourceData to a slice of papi.Hostnames /input to papi request
func mapToHostnames(givenList []interface{}) []papi.Hostname {
	var Hostnames []papi.Hostname

	for _, givenMap := range givenList {
		var r = givenMap.(map[string]interface{})
		cnameFrom := r["cname_from"]
		cnameTo := r["cname_to"]
		certProvisioningType := r["cert_provisioning_type"]
		if len(r) != 0 {
			Hostnames = append(Hostnames, papi.Hostname{
				CnameType:            "EDGE_HOSTNAME",
				CnameFrom:            cnameFrom.(string),
				CnameTo:              cnameTo.(string), // guaranteed by schema to be a string
				CertProvisioningType: certProvisioningType.(string),
			})
		}
	}
	return Hostnames
}

// Set many attributes of a schema.ResourceData in one call
func rdSetAttrs(ctx context.Context, d *schema.ResourceData, AttributeValues map[string]interface{}) error {
	logger := log.FromContext(ctx)

	for attr, value := range AttributeValues {
		if err := d.Set(attr, value); err != nil {
			logger.WithError(err).Errorf("could not set %q", attr)
			return err
		}
	}

	return nil
}

func needsUpdate(ctx context.Context, d *schema.ResourceData, formatNeedsUpdate, rulesNeedUpdate bool, rulesJSON []byte, ruleFormat string, client papi.PAPI, property papi.Property) error {
	if formatNeedsUpdate || rulesNeedUpdate {
		var Rules papi.RulesUpdate
		if err := json.Unmarshal(rulesJSON, &Rules); err != nil {
			d.Partial(true)
			return fmt.Errorf("rules are not valid JSON: %s", err)
		}

		MIME := fmt.Sprintf("application/vnd.akamai.papirules.%s+json", ruleFormat)
		h := http.Header{"Content-Type": []string{MIME}}
		ctx := session.ContextWithOptions(ctx, session.WithContextHeaders(h))

		if err := updatePropertyRules(ctx, client, property, Rules); err != nil {
			d.Partial(true)
			return err
		}
	}
	return nil
}
