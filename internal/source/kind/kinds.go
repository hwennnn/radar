package sourcekind

import "strings"

func IsSearchDiscoveryKind(kind string) bool {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "search", "search_fetch", "search_discovery", "official_careers", "google_serp_search", "x_social_search", "hackernews_jobs", "reddit_jobs_search", "linkedin_search", "linkedin_careers", "indeed_search", "wellfound_search", "talent_marketplace", "yello", "hiringcafe_jobs",
		"anthropic_careers", "databricks_careers", "oracle_careers", "ibm_careers", "cisco_careers", "tiktok_careers", "bytedance_careers", "nvidia_careers", "roblox_careers", "coinbase_careers", "ramp_careers", "discord_careers", "gitlab_careers", "twilio_careers", "samsara_careers", "airtable_careers",
		"salesforce_careers", "adobe_careers", "mongodb_careers", "servicenow_careers", "tesla_careers", "spacex_careers", "neuralink_careers", "bloomberg_careers", "shopify_careers", "uber_careers", "rippling_careers", "netflix_careers", "atlassian_careers", "canva_careers",
		"dropbox_careers", "robinhood_careers", "doordash_careers", "airbnb_careers", "palantir_careers", "lockheed_careers", "northrop_careers", "datadog_careers", "reddit_careers", "pinterest_careers", "plaid_careers", "brex_careers", "linear_careers", "asana_careers",
		"instacart_careers", "mercury_careers", "glean_careers", "cohere_careers", "anduril_careers", "deshaw_careers", "sig_careers", "virtu_careers", "hrt_careers",
		"imc_careers", "jump_careers", "twosigma_careers", "drw_careers", "fiverings_careers", "tower_research_careers", "point72_careers", "gresearch_careers", "xtx_careers", "flowtraders_careers", "radix_careers", "headlands_careers",
		"belvedere_careers", "oldmission_careers", "maven_careers", "worldquant_careers", "jpmorgan_careers", "goldman_careers", "morganstanley_careers", "capitalone_careers", "blackrock_careers", "visa_careers", "mastercard_careers", "paypal_careers", "block_careers", "affirm_careers", "chime_careers", "cursor_careers", "xai_careers", "scale_careers", "figma_careers", "vercel_careers", "notion_careers", "cloudflare_careers", "perplexity_careers", "coreweave_careers",
		"characterai_careers", "mistral_careers", "cerebras_careers", "groq_careers", "runwayml_careers", "huggingface_careers", "langchain_careers", "modal_careers", "supabase_careers", "neon_careers", "railway_careers", "planetscale_careers", "render_careers", "flyio_careers", "convex_careers", "clerk_careers", "netlify_careers", "temporal_careers", "replit_careers", "cognition_careers", "lovable_careers", "magic_careers", "poolside_careers",
		"vanta_careers", "retool_careers", "sourcegraph_careers", "postman_careers",
		"sentry_careers", "tailscale_careers", "grafana_careers", "hashicorp_careers",
		"yc_workatstartup", "simplify_jobs", "handshake_search", "builtin_jobs", "levels_fyi_jobs", "startup_jobs", "himalayas_jobs", "cord_jobs", "untapped_jobs", "climatebase_jobs", "usajobs_search", "governmentjobs_search", "mycareersfuture_sg", "nodeflair_jobs", "glints_jobs", "e27_jobs", "techinasia_jobs",
		"wantedly_jobs", "jobstreet_jobs", "jobsdb_jobs", "ctgoodjobs_jobs", "reed_uk_jobs", "totaljobs_uk_jobs", "cvlibrary_uk_jobs", "gradcracker_jobs", "ratemyplacement_jobs", "milkround_uk_jobs", "targetjobs_uk_jobs", "internsg_jobs", "gradsingapore_jobs", "jobscentral_sg_jobs", "workopolis_jobs", "jobbank_canada", "talentegg_jobs", "eluta_jobs",
		"efinancialcareers_jobs", "dice_jobs", "glassdoor_jobs", "ziprecruiter_jobs", "monster_jobs", "remoteok_jobs", "weworkremotely_jobs", "arc_dev_jobs", "remotive_jobs", "remote_co_jobs", "workingnomads_jobs", "trueup_jobs", "wayup_jobs", "ripplematch_jobs", "themuse_jobs", "simplyhired_jobs", "careerbuilder_jobs",
		"seek_jobs", "gradconnection_jobs", "prosple_jobs", "brightnetwork_jobs", "custom_url":
		return true
	}
	return strings.Contains(kind, "google_serp") ||
		strings.Contains(kind, "x_social") ||
		strings.Contains(kind, "hackernews_jobs") ||
		strings.Contains(kind, "reddit_jobs_search") ||
		strings.Contains(kind, "linkedin") ||
		strings.Contains(kind, "indeed") ||
		strings.Contains(kind, "wellfound") ||
		strings.Contains(kind, "otta") ||
		strings.Contains(kind, "talent_marketplace") ||
		strings.Contains(kind, "yello") ||
		strings.Contains(kind, "hiringcafe") ||
		strings.Contains(kind, "anthropic_careers") ||
		strings.Contains(kind, "databricks_careers") ||
		strings.Contains(kind, "oracle_careers") ||
		strings.Contains(kind, "ibm_careers") ||
		strings.Contains(kind, "cisco_careers") ||
		strings.Contains(kind, "tiktok_careers") ||
		strings.Contains(kind, "bytedance_careers") ||
		strings.Contains(kind, "nvidia_careers") ||
		strings.Contains(kind, "roblox_careers") ||
		strings.Contains(kind, "coinbase_careers") ||
		strings.Contains(kind, "ramp_careers") ||
		strings.Contains(kind, "discord_careers") ||
		strings.Contains(kind, "gitlab_careers") ||
		strings.Contains(kind, "twilio_careers") ||
		strings.Contains(kind, "samsara_careers") ||
		strings.Contains(kind, "airtable_careers") ||
		strings.Contains(kind, "salesforce_careers") ||
		strings.Contains(kind, "adobe_careers") ||
		strings.Contains(kind, "mongodb_careers") ||
		strings.Contains(kind, "servicenow_careers") ||
		strings.Contains(kind, "tesla_careers") ||
		strings.Contains(kind, "spacex_careers") ||
		strings.Contains(kind, "neuralink_careers") ||
		strings.Contains(kind, "bloomberg_careers") ||
		strings.Contains(kind, "shopify_careers") ||
		strings.Contains(kind, "uber_careers") ||
		strings.Contains(kind, "rippling_careers") ||
		strings.Contains(kind, "netflix_careers") ||
		strings.Contains(kind, "atlassian_careers") ||
		strings.Contains(kind, "canva_careers") ||
		strings.Contains(kind, "dropbox_careers") ||
		strings.Contains(kind, "robinhood_careers") ||
		strings.Contains(kind, "doordash_careers") ||
		strings.Contains(kind, "airbnb_careers") ||
		strings.Contains(kind, "palantir_careers") ||
		strings.Contains(kind, "lockheed_careers") ||
		strings.Contains(kind, "northrop_careers") ||
		strings.Contains(kind, "datadog_careers") ||
		strings.Contains(kind, "reddit_careers") ||
		strings.Contains(kind, "pinterest_careers") ||
		strings.Contains(kind, "plaid_careers") ||
		strings.Contains(kind, "brex_careers") ||
		strings.Contains(kind, "linear_careers") ||
		strings.Contains(kind, "asana_careers") ||
		strings.Contains(kind, "instacart_careers") ||
		strings.Contains(kind, "mercury_careers") ||
		strings.Contains(kind, "glean_careers") ||
		strings.Contains(kind, "cohere_careers") ||
		strings.Contains(kind, "anduril_careers") ||
		strings.Contains(kind, "deshaw_careers") ||
		strings.Contains(kind, "sig_careers") ||
		strings.Contains(kind, "virtu_careers") ||
		strings.Contains(kind, "hrt_careers") ||
		strings.Contains(kind, "imc_careers") ||
		strings.Contains(kind, "jump_careers") ||
		strings.Contains(kind, "twosigma_careers") ||
		strings.Contains(kind, "drw_careers") ||
		strings.Contains(kind, "fiverings_careers") ||
		strings.Contains(kind, "tower_research_careers") ||
		strings.Contains(kind, "point72_careers") ||
		strings.Contains(kind, "gresearch_careers") ||
		strings.Contains(kind, "xtx_careers") ||
		strings.Contains(kind, "flowtraders_careers") ||
		strings.Contains(kind, "radix_careers") ||
		strings.Contains(kind, "headlands_careers") ||
		strings.Contains(kind, "belvedere_careers") ||
		strings.Contains(kind, "oldmission_careers") ||
		strings.Contains(kind, "maven_careers") ||
		strings.Contains(kind, "worldquant_careers") ||
		strings.Contains(kind, "jpmorgan_careers") ||
		strings.Contains(kind, "goldman_careers") ||
		strings.Contains(kind, "morganstanley_careers") ||
		strings.Contains(kind, "capitalone_careers") ||
		strings.Contains(kind, "blackrock_careers") ||
		strings.Contains(kind, "visa_careers") ||
		strings.Contains(kind, "mastercard_careers") ||
		strings.Contains(kind, "paypal_careers") ||
		strings.Contains(kind, "block_careers") ||
		strings.Contains(kind, "affirm_careers") ||
		strings.Contains(kind, "chime_careers") ||
		strings.Contains(kind, "cursor_careers") ||
		strings.Contains(kind, "xai_careers") ||
		strings.Contains(kind, "scale_careers") ||
		strings.Contains(kind, "figma_careers") ||
		strings.Contains(kind, "vercel_careers") ||
		strings.Contains(kind, "notion_careers") ||
		strings.Contains(kind, "cloudflare_careers") ||
		strings.Contains(kind, "perplexity_careers") ||
		strings.Contains(kind, "coreweave_careers") ||
		strings.Contains(kind, "characterai_careers") ||
		strings.Contains(kind, "mistral_careers") ||
		strings.Contains(kind, "cerebras_careers") ||
		strings.Contains(kind, "groq_careers") ||
		strings.Contains(kind, "runwayml_careers") ||
		strings.Contains(kind, "huggingface_careers") ||
		strings.Contains(kind, "langchain_careers") ||
		strings.Contains(kind, "modal_careers") ||
		strings.Contains(kind, "supabase_careers") ||
		strings.Contains(kind, "neon_careers") ||
		strings.Contains(kind, "railway_careers") ||
		strings.Contains(kind, "planetscale_careers") ||
		strings.Contains(kind, "render_careers") ||
		strings.Contains(kind, "flyio_careers") ||
		strings.Contains(kind, "convex_careers") ||
		strings.Contains(kind, "clerk_careers") ||
		strings.Contains(kind, "netlify_careers") ||
		strings.Contains(kind, "temporal_careers") ||
		strings.Contains(kind, "replit_careers") ||
		strings.Contains(kind, "cognition_careers") ||
		strings.Contains(kind, "lovable_careers") ||
		strings.Contains(kind, "magic_careers") ||
		strings.Contains(kind, "poolside_careers") ||
		strings.Contains(kind, "vanta_careers") ||
		strings.Contains(kind, "retool_careers") ||
		strings.Contains(kind, "sourcegraph_careers") ||
		strings.Contains(kind, "postman_careers") ||
		strings.Contains(kind, "sentry_careers") ||
		strings.Contains(kind, "tailscale_careers") ||
		strings.Contains(kind, "grafana_careers") ||
		strings.Contains(kind, "hashicorp_careers") ||
		strings.Contains(kind, "yc_workatstartup") ||
		strings.Contains(kind, "simplify_jobs") ||
		strings.Contains(kind, "handshake") ||
		strings.Contains(kind, "builtin_jobs") ||
		strings.Contains(kind, "levels_fyi") ||
		strings.Contains(kind, "startup_jobs") ||
		strings.Contains(kind, "himalayas") ||
		strings.Contains(kind, "cord_jobs") ||
		strings.Contains(kind, "untapped_jobs") ||
		strings.Contains(kind, "climatebase_jobs") ||
		strings.Contains(kind, "usajobs") ||
		strings.Contains(kind, "governmentjobs") ||
		strings.Contains(kind, "mycareersfuture") ||
		strings.Contains(kind, "nodeflair") ||
		strings.Contains(kind, "glints") ||
		strings.Contains(kind, "e27_jobs") ||
		strings.Contains(kind, "techinasia") ||
		strings.Contains(kind, "wantedly") ||
		strings.Contains(kind, "jobstreet") ||
		strings.Contains(kind, "jobsdb") ||
		strings.Contains(kind, "ctgoodjobs") ||
		strings.Contains(kind, "reed_uk") ||
		strings.Contains(kind, "totaljobs_uk") ||
		strings.Contains(kind, "cvlibrary_uk") ||
		strings.Contains(kind, "gradcracker") ||
		strings.Contains(kind, "ratemyplacement") ||
		strings.Contains(kind, "milkround_uk") ||
		strings.Contains(kind, "targetjobs_uk") ||
		strings.Contains(kind, "internsg") ||
		strings.Contains(kind, "gradsingapore") ||
		strings.Contains(kind, "jobscentral_sg") ||
		strings.Contains(kind, "workopolis") ||
		strings.Contains(kind, "jobbank") ||
		strings.Contains(kind, "talentegg") ||
		strings.Contains(kind, "eluta") ||
		strings.Contains(kind, "efinancialcareers") ||
		strings.Contains(kind, "dice_jobs") ||
		strings.Contains(kind, "glassdoor") ||
		strings.Contains(kind, "ziprecruiter") ||
		strings.Contains(kind, "monster_jobs") ||
		strings.Contains(kind, "remoteok_jobs") ||
		strings.Contains(kind, "weworkremotely_jobs") ||
		strings.Contains(kind, "arc_dev_jobs") ||
		strings.Contains(kind, "remotive_jobs") ||
		strings.Contains(kind, "remote_co_jobs") ||
		strings.Contains(kind, "workingnomads_jobs") ||
		strings.Contains(kind, "trueup_jobs") ||
		strings.Contains(kind, "wayup_jobs") ||
		strings.Contains(kind, "ripplematch_jobs") ||
		strings.Contains(kind, "themuse_jobs") ||
		strings.Contains(kind, "simplyhired_jobs") ||
		strings.Contains(kind, "careerbuilder_jobs") ||
		strings.Contains(kind, "seek_jobs") ||
		strings.Contains(kind, "gradconnection_jobs") ||
		strings.Contains(kind, "prosple_jobs") ||
		strings.Contains(kind, "brightnetwork_jobs") ||
		strings.Contains(kind, "custom_url") ||
		strings.Contains(kind, "search")
}
