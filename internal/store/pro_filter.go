package store

import "strings"

// Use the time the account entered Pro management, not later status refreshes.
// Old imports without that timestamp fall back to their original creation time.
const proManagedTimeSQL = `COALESCE(
	julianday(NULLIF(json_extract(profile,'$.pro_managed_at'),'0001-01-01T00:00:00Z')),
	julianday(NULLIF(json_extract(profile,'$.created_at'),'0001-01-01T00:00:00Z')))`

// Listing, counts and filtered export share the same partition. Defaults such
// as an empty ProAuto object or pending merge steps do not mean work started.
// Include persisted manual/legacy progress, matching the stages shown by the UI.
// A completed login alone is preparation, not an in-progress workflow. Actual
// automation jobs and running/failed login attempts retain their progress state.
// The payment lookup uses the existing email index and never reads credentials.
const proAccountsCTE = `WITH accounts AS (
	SELECT email, profile, encrypted_credentials, updated_at,
  CASE WHEN json_extract(profile,'$.pro_dead') IS NOT NULL THEN 'dead'
  WHEN json_extract(profile,'$.pro_merge_v2.completed_at') IS NOT NULL THEN 'merge_v2'
  WHEN json_extract(profile,'$.pro_merchant.completed_at') IS NOT NULL THEN 'merchant'
  WHEN json_extract(profile,'$.pro_deactivation.accepted_at') IS NOT NULL OR json_extract(profile,'$.pro_deactivation.confirmed_at') IS NOT NULL THEN 'deleted'
  WHEN json_extract(profile,'$.space_merged_once')=1 THEN 'merged'
  WHEN json_extract(profile,'$.pro_merge_v2') IS NOT NULL OR COALESCE(json_extract(profile,'$.pro_merchant.request_id'),'')<>'' OR COALESCE(json_extract(profile,'$.pro_deactivation.status'),'')<>''
   OR COALESCE(json_extract(profile,'$.pro_auto.id'),'')<>''
			OR COALESCE(json_extract(profile,'$.pro_auto.status'),'') NOT IN ('','not_started','pending','stopped')
			OR COALESCE(json_extract(profile,'$.pro_auto.stage'),'')<>''
			OR EXISTS (SELECT 1 FROM json_each(profile,'$.pro_auto.steps') WHERE COALESCE(value,'') NOT IN ('','not_started','pending') AND NOT (key='login' AND value='completed'))
			OR EXISTS (SELECT 1 FROM json_each(profile,'$.pro_manual_stages') WHERE COALESCE(json_extract(value,'$.status'),'') NOT IN ('','not_started','pending') AND NOT (key='login' AND json_extract(value,'$.status')='completed'))
			OR json_extract(profile,'$.pro_workflow_running')=1
			OR COALESCE(json_extract(profile,'$.pro_invite_status'),'') NOT IN ('','not_started','pending')
			OR COALESCE(json_extract(profile,'$.pro_accept_status'),'') NOT IN ('','not_started','pending')
			OR COALESCE(json_extract(profile,'$.pro_transfer_status'),'') NOT IN ('','not_started','pending')
			OR COALESCE(json_extract(profile,'$.pro_remove_status'),'') NOT IN ('','not_started','pending')
			OR COALESCE(json_extract(profile,'$.push_status'),'') NOT IN ('','not_started','pending')
			OR COALESCE(json_extract(profile,'$.quota_status'),'') NOT IN ('','not_started','pending')
			OR (COALESCE(json_extract(profile,'$.oauth_authorized_at'),'')<>'' AND json_extract(profile,'$.access_token_present')=1 AND json_extract(profile,'$.refresh_token_present')=1)
			OR EXISTS (SELECT 1 FROM gptpay_orders o WHERE o.email=mail_accounts.email)
		THEN 'in_progress' ELSE 'unmerged' END AS merge_state,
		LOWER(email || ' ' || COALESCE(json_extract(profile,'$.current_plan_type'),'') || ' ' || COALESCE(json_extract(profile,'$.push_provider'),'')) AS search_text,
		` + proManagedTimeSQL + ` AS managed_at,
		COALESCE(
			julianday(NULLIF(json_extract(profile,'$.pro_auto.started_at'),'0001-01-01T00:00:00Z')),
			(SELECT MIN(julianday(NULLIF(json_extract(value,'$.started_at'),'0001-01-01T00:00:00Z'))) FROM json_each(profile,'$.pro_manual_stages') WHERE COALESCE(json_extract(value,'$.status'),'') NOT IN ('','not_started','pending') AND NOT (key='login' AND json_extract(value,'$.status')='completed')),
			(SELECT MIN(julianday(NULLIF(json_extract(o.profile,'$.created_at'),'0001-01-01T00:00:00Z'))) FROM gptpay_orders o WHERE o.email=mail_accounts.email),
			julianday(NULLIF(json_extract(profile,'$.pro_deactivation.started_at'),'0001-01-01T00:00:00Z')),
			julianday(NULLIF(json_extract(profile,'$.oauth_authorized_at'),'0001-01-01T00:00:00Z')),
			julianday(NULLIF(json_extract(profile,'$.pro_push_started_at'),'0001-01-01T00:00:00Z'))
		) AS progress_at,
		COALESCE(
			julianday(NULLIF(json_extract(profile,'$.pro_auto.stage_times."recharge:completed"'),'0001-01-01T00:00:00Z')),
			CASE WHEN json_extract(profile,'$.pro_manual_stages.recharge.status')='completed' THEN julianday(NULLIF(json_extract(profile,'$.pro_manual_stages.recharge.updated_at'),'0001-01-01T00:00:00Z')) END,
			(SELECT MAX(julianday(NULLIF(json_extract(o.profile,'$.succeeded_at'),'0001-01-01T00:00:00Z'))) FROM gptpay_orders o WHERE o.email=mail_accounts.email AND json_extract(o.profile,'$.status')='success'),
			(SELECT MAX(julianday(NULLIF(json_extract(o.profile,'$.updated_at'),'0001-01-01T00:00:00Z'))) FROM gptpay_orders o WHERE o.email=mail_accounts.email AND json_extract(o.profile,'$.status')='success')
		) AS activated_at,
		julianday(NULLIF(json_extract(profile,'$.space_merged_at'),'0001-01-01T00:00:00Z')) AS merged_at,
		COALESCE(
			julianday(NULLIF(json_extract(profile,'$.pro_deactivation.accepted_at'),'0001-01-01T00:00:00Z')),
			julianday(NULLIF(json_extract(profile,'$.pro_deactivation.confirmed_at'),'0001-01-01T00:00:00Z'))
		) AS deleted_at, julianday(json_extract(profile,'$.pro_merchant.completed_at')) AS merchant_at, julianday(json_extract(profile,'$.pro_merge_v2.started_at')) AS merge_v2_at
	FROM mail_accounts WHERE LOWER(COALESCE(json_extract(profile,'$.management_scope'),''))='pro'
)`

func proAccountsOrderSQL(state string) string {
	switch state {
	case "unmerged":
		return "managed_at IS NULL, managed_at ASC, email ASC"
	case "in_progress":
		return "COALESCE(progress_at,activated_at) DESC, managed_at DESC, email ASC"
	case "merged":
		return "merged_at DESC, managed_at DESC, email ASC"
	case "merge_v2":
		return "merge_v2_at DESC, managed_at DESC, email ASC"
	case "merchant":
		return "merchant_at DESC, managed_at DESC, email ASC"
	case "dead":
		return "julianday(json_extract(profile,'$.pro_dead.at')) DESC, email ASC"
	case "deleted":
		return "deleted_at DESC, managed_at DESC, email ASC"
	default:
		return "managed_at DESC, email ASC"
	}
}

// Expose the same timestamp used for sorting without persisting list-only data.
func proAccountsEventTimeSQL(state string) string {
	switch state {
	case "in_progress":
		return "COALESCE(progress_at,activated_at)"
	case "merged":
		return "merged_at"
	case "merge_v2":
		return "merge_v2_at"
	case "merchant":
		return "merchant_at"
	case "dead":
		return "julianday(json_extract(profile,'$.pro_dead.at'))"
	case "deleted":
		return "deleted_at"
	default:
		return "managed_at"
	}
}

func normalizeProMergeFilter(value string) string {
	switch value = strings.ToLower(strings.TrimSpace(value)); value {
	case "unmerged", "in_progress", "merged", "deleted", "merchant", "merge_v2", "dead":
		return value
	default:
		return ""
	}
}
