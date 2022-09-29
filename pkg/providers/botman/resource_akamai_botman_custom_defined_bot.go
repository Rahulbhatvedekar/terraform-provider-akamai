package botman

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/akamai/AkamaiOPEN-edgegrid-golang/v2/pkg/botman"
	"github.com/akamai/terraform-provider-akamai/v2/pkg/akamai"
	"github.com/akamai/terraform-provider-akamai/v2/pkg/tools"

	"github.com/hashicorp/terraform-plugin-sdk/v2/diag"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/customdiff"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/validation"
)

func resourceCustomDefinedBot() *schema.Resource {
	return &schema.Resource{
		CreateContext: resourceCustomDefinedBotCreate,
		ReadContext:   resourceCustomDefinedBotRead,
		UpdateContext: resourceCustomDefinedBotUpdate,
		DeleteContext: resourceCustomDefinedBotDelete,
		CustomizeDiff: customdiff.All(
			verifyConfigIDUnchanged,
		),
		Importer: &schema.ResourceImporter{
			StateContext: schema.ImportStatePassthroughContext,
		},
		Schema: map[string]*schema.Schema{
			"config_id": {
				Type:     schema.TypeInt,
				Required: true,
			},
			"bot_id": {
				Type:     schema.TypeString,
				Computed: true,
			},
			"custom_defined_bot": {
				Type:             schema.TypeString,
				Required:         true,
				ValidateDiagFunc: validation.ToDiagFunc(validation.StringIsJSON),
				DiffSuppressFunc: suppressEquivalentJSONDiffsGeneric,
			},
		},
	}
}

func resourceCustomDefinedBotCreate(ctx context.Context, d *schema.ResourceData, m interface{}) diag.Diagnostics {
	meta := akamai.Meta(m)
	client := inst.Client(meta)
	logger := meta.Log("botman", "resourceCustomDefinedBotCreate")
	logger.Debugf("in resourceCustomDefinedBotCreate")

	configID, err := tools.GetIntValue("config_id", d)
	if err != nil {
		return diag.FromErr(err)
	}

	version, err := getModifiableConfigVersion(ctx, configID, "CustomDefinedBot", m)
	if err != nil {
		return diag.FromErr(err)
	}

	jsonPayloadString, err := tools.GetStringValue("custom_defined_bot", d)
	if err != nil {
		return diag.FromErr(err)
	}

	request := botman.CreateCustomDefinedBotRequest{
		ConfigID:    int64(configID),
		Version:     int64(version),
		JsonPayload: json.RawMessage(jsonPayloadString),
	}

	response, err := client.CreateCustomDefinedBot(ctx, request)
	if err != nil {
		logger.Errorf("calling 'CreateCustomDefinedBot': %s", err.Error())
		return diag.FromErr(err)
	}

	d.SetId(fmt.Sprintf("%d:%s", configID, (response)["botId"].(string)))

	return resourceCustomDefinedBotRead(ctx, d, m)
}

func resourceCustomDefinedBotRead(ctx context.Context, d *schema.ResourceData, m interface{}) diag.Diagnostics {
	meta := akamai.Meta(m)
	client := inst.Client(meta)
	logger := meta.Log("botman", "resourceCustomDefinedBotRead")
	logger.Debugf("in resourceCustomDefinedBotRead")

	iDParts, err := splitID(d.Id(), 2, "configID:botID")
	if err != nil {
		return diag.FromErr(err)
	}

	configID, err := strconv.Atoi(iDParts[0])
	if err != nil {
		return diag.FromErr(err)
	}

	version, err := getLatestConfigVersion(ctx, configID, m)
	if err != nil {
		return diag.FromErr(err)
	}

	botID := iDParts[1]

	request := botman.GetCustomDefinedBotRequest{
		ConfigID: int64(configID),
		Version:  int64(version),
		BotID:    botID,
	}

	response, err := client.GetCustomDefinedBot(ctx, request)
	if err != nil {
		logger.Errorf("calling 'GetCustomDefinedBot': %s", err.Error())
		return diag.FromErr(err)
	}

	// Removing botId from response to suppress diff
	delete(response, "botId")

	jsonBody, err := json.Marshal(response)
	if err != nil {
		return diag.FromErr(err)
	}
	fields := map[string]interface{}{
		"config_id":          configID,
		"bot_id":             botID,
		"custom_defined_bot": string(jsonBody),
	}
	if err := tools.SetAttrs(d, fields); err != nil {
		return diag.Errorf("%s: %s", tools.ErrValueSet, err.Error())
	}

	return nil
}

func resourceCustomDefinedBotUpdate(ctx context.Context, d *schema.ResourceData, m interface{}) diag.Diagnostics {
	meta := akamai.Meta(m)
	client := inst.Client(meta)
	logger := meta.Log("botman", "resourceCustomDefinedBotUpdate")
	logger.Debugf("in resourceCustomDefinedBotUpdate")

	iDParts, err := splitID(d.Id(), 2, "configID:botID")
	if err != nil {
		return diag.FromErr(err)
	}

	configID, err := strconv.Atoi(iDParts[0])
	if err != nil {
		return diag.FromErr(err)
	}

	version, err := getModifiableConfigVersion(ctx, configID, "CustomDefinedBot", m)
	if err != nil {
		return diag.FromErr(err)
	}

	botID := iDParts[1]

	jsonPayload, err := getJSONPayload(d, "custom_defined_bot", "botId", botID)
	if err != nil {
		return diag.FromErr(err)
	}

	request := botman.UpdateCustomDefinedBotRequest{
		ConfigID:    int64(configID),
		Version:     int64(version),
		BotID:       botID,
		JsonPayload: jsonPayload,
	}

	_, err = client.UpdateCustomDefinedBot(ctx, request)
	if err != nil {
		logger.Errorf("calling 'UpdateCustomDefinedBot': %s", err.Error())
		return diag.FromErr(err)
	}

	return resourceCustomDefinedBotRead(ctx, d, m)
}

func resourceCustomDefinedBotDelete(ctx context.Context, d *schema.ResourceData, m interface{}) diag.Diagnostics {
	meta := akamai.Meta(m)
	client := inst.Client(meta)
	logger := meta.Log("botman", "resourceCustomDefinedBotDelete")
	logger.Debugf("in resourceCustomDefinedBotDelete")

	iDParts, err := splitID(d.Id(), 2, "configID:botID")
	if err != nil {
		return diag.FromErr(err)
	}

	configID, err := strconv.Atoi(iDParts[0])
	if err != nil {
		return diag.FromErr(err)
	}

	version, err := getModifiableConfigVersion(ctx, configID, "CustomDefinedBot", m)
	if err != nil {
		return diag.FromErr(err)
	}

	botID := iDParts[1]

	request := botman.RemoveCustomDefinedBotRequest{
		ConfigID: int64(configID),
		Version:  int64(version),
		BotID:    botID,
	}

	err = client.RemoveCustomDefinedBot(ctx, request)
	if err != nil {
		logger.Errorf("calling 'RemoveCustomDefinedBot': %s", err.Error())
		return diag.FromErr(err)
	}
	return nil
}