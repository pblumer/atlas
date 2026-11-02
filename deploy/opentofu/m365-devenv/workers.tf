# ---------------------------------------------------------------------------
# One description of every worker, in the shapes Atlas reads them:
#   - as a Console worker (kind, endpoint, provider, sender, and a vault bundle), which
#     the Atlas server's bootstrap creates through the API (scripts/atlas-bootstrap.py);
#   - as ATLAS_* variables for a worker run by hand elsewhere (out/worker.env).
# The bundle fields are the ones api/superviseenv.go, api/exchangeworkerenv.go,
# connector/sharepoint and connector/mail parse.
# ---------------------------------------------------------------------------

locals {
  mail_sender = var.mail_sender != "" ? var.mail_sender : azuread_user.manager.user_principal_name

  # The domain controller as the Atlas server sees it (inside the virtual network)
  # and as a worker elsewhere sees it (its public name).
  ad_internal_host = "dc01.${var.ad_domain_name}"
  ad_url_internal  = "ldaps://${local.ad_internal_host}:636"
  ad_url_public    = var.enable_ad_dc ? "ldaps://${local.ad_public_fqdn}:636" : null

  worker_defs = concat(
    var.workers.entra ? [{
      key      = "entra"
      name     = var.worker_names.entra
      kind     = "entra"
      endpoint = ""
      provider = ""
      sender   = ""
      bundle = jsonencode(merge(
        { tenantId = var.tenant_id, clientId = module.entra[0].client_id },
        var.entra_credential == "certificate"
        ? { certificate = module.entra[0].certificate_bundle_pem }
        : { clientSecret = module.entra[0].client_secret },
      ))
    }] : [],
    var.workers.exchange ? [{
      key      = "exchange"
      name     = var.worker_names.exchange
      kind     = "exchange"
      endpoint = ""
      provider = ""
      sender   = ""
      bundle = jsonencode({
        organization = local.initial_domain
        tenantId     = var.tenant_id
        clientId     = module.exchange[0].client_id
        certificate  = module.exchange[0].certificate_bundle_pem
      })
    }] : [],
    var.workers.sharepoint ? [{
      key      = "sharepoint"
      name     = var.worker_names.sharepoint
      kind     = "sharepoint"
      endpoint = ""
      provider = ""
      sender   = ""
      bundle = jsonencode(merge(
        { method = "clientCredentials", tenantId = var.tenant_id, clientId = module.sharepoint[0].client_id },
        var.sharepoint_credential == "certificate"
        ? { certificate = module.sharepoint[0].certificate_bundle_pem }
        : { clientSecret = module.sharepoint[0].client_secret },
      ))
    }] : [],
    var.workers.teamsphone ? [{
      key      = "teamsphone"
      name     = var.worker_names.teamsphone
      kind     = "teamsphone"
      endpoint = ""
      provider = ""
      sender   = ""
      bundle = jsonencode({
        tenantId    = var.tenant_id
        clientId    = module.teamsphone[0].client_id
        certificate = module.teamsphone[0].certificate_bundle_pem
      })
    }] : [],
    var.workers.mail ? [{
      key      = "mail"
      name     = var.worker_names.mail
      kind     = "mail"
      endpoint = ""
      provider = "microsoft"
      sender   = local.mail_sender
      bundle = jsonencode({
        method       = "clientCredentials"
        tenantId     = var.tenant_id
        clientId     = module.mail[0].client_id
        clientSecret = module.mail[0].client_secret
      })
    }] : [],
    var.enable_ad_dc ? [{
      key      = "ad"
      name     = var.worker_names.ad
      kind     = "ad"
      endpoint = local.ad_url_internal
      provider = ""
      sender   = ""
      bundle   = jsonencode({ bindDN = local.ad_bind_dn, password = random_password.ad["service"].result })
    }] : [],
  )
}
