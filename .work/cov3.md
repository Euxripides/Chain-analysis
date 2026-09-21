# Commit-gate write coverage with external I/O (mechanically enumerated)

- registered routes: 378
- state-changing routes: 163
- session-permission guarded writes: 145

- gated by external class bus: 8
- gated by external class bus,clickhouse,object: 2
- gated by external class clickhouse: 19
- gated by external class clickhouse,object: 3
- gated by external class clickhouse,object,provider: 2
- gated by external class db-only: 101
- gated by external class object: 4
- gated by external class provider: 6

| method | path | classification | permission | handler | external |
| --- | --- | --- | --- | --- | --- |
| POST | `/api/admin/v1/alerts/{id}/commands/{command}` | commit-gate | admin:alert:manage | handleAdminAlertCommand | clickhouse |
| POST | `/api/admin/v1/api-keys` | commit-gate | admin:apikey:create | handleAdminCreateAPIKey | - |
| POST | `/api/admin/v1/api-keys/{id}/commands/{command}` | commit-gate | admin:apikey:revoke | handleAdminAPIKeyCommand | clickhouse |
| POST | `/api/admin/v1/audit/anchors` | commit-gate | admin:audit:seal | handleAdminAuditAnchorCreate | object |
| POST | `/api/admin/v1/audit/export` | commit-gate | admin:audit:export | handleAdminAuditExport | object |
| POST | `/api/admin/v1/audit/integrity/incidents/requests` | commit-gate | admin:audit:seal | handleAdminAuditIntegrityRequest | clickhouse |
| POST | `/api/admin/v1/audit/integrity/incidents/requests/{id}/approve` | commit-gate | admin:approval:decide | handleAdminAuditIntegrityApprove | clickhouse |
| POST | `/api/admin/v1/audit/integrity/incidents/requests/{id}/execute` | commit-gate | admin:audit:seal | handleAdminAuditIntegrityExecute | clickhouse,object |
| POST | `/api/admin/v1/audit/integrity/incidents/requests/{id}/reject` | commit-gate | admin:approval:decide | handleAdminAuditIntegrityReject | clickhouse |
| POST | `/api/admin/v1/audit/integrity/preview` | commit-gate | admin:audit:verify | handleAdminAuditIntegrityPreview | - |
| POST | `/api/admin/v1/audit/verify` | commit-gate | admin:audit:verify | handleAdminAuditVerify | - |
| POST | `/api/admin/v1/backups` | commit-gate | admin:backup:create | handleAdminCreateBackup | object |
| POST | `/api/admin/v1/backups/{id}/commands/verify` | commit-gate | admin:backup:read | handleAdminVerifyBackup | clickhouse,object |
| POST | `/api/admin/v1/budgets` | commit-gate | admin:budget:update | handleAdminCreateBudget | - |
| PATCH | `/api/admin/v1/budgets/{id}` | commit-gate | admin:budget:update | handleAdminUpdateBudget | clickhouse |
| POST | `/api/admin/v1/budgets/{id}/commands/evaluate` | commit-gate | admin:budget:read | handleAdminEvaluateBudget | clickhouse |
| POST | `/api/admin/v1/control/kill-switch/preview` | commit-gate | admin:killswitch:engage | handleAdminKillSwitchPreview | - |
| POST | `/api/admin/v1/control/kill-switch/requests` | commit-gate | admin:killswitch:engage | handleAdminKillSwitchRequest | clickhouse |
| POST | `/api/admin/v1/control/kill-switch/requests/{id}/approve` | commit-gate | admin:approval:decide | handleAdminKillSwitchApprove | clickhouse |
| POST | `/api/admin/v1/control/kill-switch/requests/{id}/execute` | commit-gate | admin:killswitch:engage | handleAdminKillSwitchExecute | bus,clickhouse,object |
| POST | `/api/admin/v1/control/kill-switch/requests/{id}/reject` | commit-gate | admin:approval:decide | handleAdminKillSwitchReject | clickhouse |
| POST | `/api/admin/v1/control/kill-switch/requests/{id}/restore` | commit-gate | admin:killswitch:release | handleAdminKillSwitchRestore | bus,clickhouse,object |
| PATCH | `/api/admin/v1/control/policies` | commit-gate | admin:killswitch:engage | handleAdminUpdateControlPolicies | - |
| POST | `/api/admin/v1/deletion-requests` | commit-gate | admin:deletion:request | handleAdminCreateDeletionRequest | clickhouse |
| POST | `/api/admin/v1/deletion-requests/{id}/commands/{command}` | commit-gate | admin:deletion:approve | handleAdminDeletionCommand | clickhouse |
| POST | `/api/admin/v1/feature-flags` | commit-gate | admin:flag:update | handleAdminUpsertFeatureFlag | - |
| PATCH | `/api/admin/v1/feature-flags/{key}` | commit-gate | admin:flag:update | handleAdminUpsertFeatureFlag | - |
| POST | `/api/admin/v1/feature-flags/{key}/commands/{command}` | commit-gate | admin:flag:update | handleAdminFlagCommand | clickhouse |
| POST | `/api/admin/v1/jobs/{id}/commands/{command}` | commit-gate | admin:job:control | handleAdminJobCommand | - |
| POST | `/api/admin/v1/observability/alerts/evaluate` | commit-gate | admin:metrics:read | handleAdminEvaluateAlerts | - |
| PATCH | `/api/admin/v1/organizations/{id}` | commit-gate | admin:org:manage | handleAdminUpdateOrganization | - |
| PATCH | `/api/admin/v1/providers/{id}` | commit-gate | admin:provider:update | handleAdminProviderUpdate | provider |
| POST | `/api/admin/v1/providers/{id}/commands/{command}` | commit-gate |  |  | - |
| POST | `/api/admin/v1/restores` | commit-gate | admin:backup:restore | handleAdminCreateRestore | clickhouse |
| POST | `/api/admin/v1/restores/preview` | commit-gate | admin:backup:restore | handleAdminRestorePreview | clickhouse,object |
| POST | `/api/admin/v1/retention-policies/preview` | commit-gate | admin:retention:read | handleAdminRetentionPreview | clickhouse |
| PATCH | `/api/admin/v1/retention-policies/{id}` | commit-gate | admin:retention:manage | handleAdminUpdateRetentionPolicy | clickhouse |
| PATCH | `/api/admin/v1/routing-policies/{id}` | commit-gate | admin:routing:update | handleAdminRoutingPolicyUpdate | - |
| POST | `/api/admin/v1/runtime/performance/commands/{command}` | commit-gate | admin:performance:update | handleAdminPerformanceCommand | - |
| POST | `/api/admin/v1/runtime/performance/preview` | commit-gate | admin:performance:update | handleAdminPerformancePreview | - |
| POST | `/api/admin/v1/sessions/{id}/commands/revoke` | commit-gate-self-target | admin:session:revoke | handleAdminRevokeSession | bus |
| PATCH | `/api/admin/v1/settings/{key}` | commit-gate | admin:config:update | handleAdminUpdateSetting | - |
| POST | `/api/admin/v1/settings/{key}/commands/{command}` | commit-gate | admin:config:update | handleAdminSettingCommand | - |
| POST | `/api/admin/v1/users` | commit-gate | admin:user:create | handleAdminCreateUser | - |
| PATCH | `/api/admin/v1/users/{id}` | commit-gate | admin:user:update | handleAdminUpdateUser | - |
| POST | `/api/admin/v1/users/{id}/commands/disable` | commit-gate | admin:user:disable | handleAdminDisableUser | - |
| POST | `/api/admin/v1/users/{id}/commands/enable` | commit-gate | admin:user:disable | handleAdminEnableUser | - |
| POST | `/api/admin/v1/users/{id}/commands/reset-password` | commit-gate | admin:user:password:reset | handleAdminResetPassword | - |
| POST | `/api/admin/v1/users/{id}/roles` | commit-gate | admin:member:manage | handleAdminGrantRole | - |
| DELETE | `/api/admin/v1/users/{id}/roles/{binding}` | commit-gate | admin:member:manage | handleAdminRevokeRole | - |
| POST | `/api/admin/v1/workspaces` | commit-gate | admin:workspace:manage | handleAdminCreateWorkspace | - |
| PATCH | `/api/admin/v1/workspaces/{id}` | commit-gate | admin:workspace:manage | handleAdminUpdateWorkspace | - |
| PATCH | `/api/auth/v1/devices/{id}` | self-service |  | handleAuthUpdateDevice | bus |
| POST | `/api/auth/v1/login` | self-service |  | handleAuthLogin | - |
| POST | `/api/auth/v1/logout` | self-service |  | handleAuthLogout | bus |
| POST | `/api/auth/v1/mfa/verify` | self-service |  | handleNotImplemented | - |
| POST | `/api/auth/v1/password/change` | self-service |  | handleAuthChangePassword | bus |
| POST | `/api/auth/v1/refresh` | self-service |  | handleAuthRefresh | bus |
| POST | `/api/auth/v1/session/workspace` | self-service |  | handleAuthSwitchWorkspace | bus |
| POST | `/api/auth/v1/sessions/revoke-all` | self-service |  | handleAuthRevokeAll | bus |
| DELETE | `/api/auth/v1/sessions/{id}` | self-service |  | handleAuthRevokeSession | bus |
| POST | `/api/v1/addresses/batch-assets` | actions-bearer |  | handleActionBatchAssets | clickhouse |
| POST | `/api/v1/addresses/batch-overview` | actions-bearer |  | handleActionBatchOverview | clickhouse |
| PATCH | `/api/v1/addresses/{chain}/{address}/metadata` | commit-gate | admin:dataset:manage | handleAddressMetadata | - |
| POST | `/api/v1/admin/action-api-keys/` | actions-bearer |  | handleCreateActionKey | - |
| DELETE | `/api/v1/admin/action-api-keys/{id}` | actions-bearer |  | handleDeleteActionKey | - |
| POST | `/api/v1/admin/action-api-keys/{id}/disable` | actions-bearer |  | handleDisableActionKey | - |
| POST | `/api/v1/admin/ankr/benchmarks` | commit-gate | admin:performance:update | handleBenchmarkCreate | clickhouse |
| POST | `/api/v1/admin/ankr/range-certifications` | commit-gate | admin:performance:update | handleRecordRangeCertification | - |
| POST | `/api/v1/admin/contracts/{chain}/{address}/abi` | commit-gate | admin:dataset:manage | handleUploadABI | provider |
| POST | `/api/v1/admin/methods/backfill` | commit-gate | admin:dataset:manage | handleCreateBackfill | - |
| POST | `/api/v1/admin/methods/backfill/{id}/{action}` | commit-gate | admin:dataset:manage | handleControlBackfill | - |
| POST | `/api/v1/admin/methods/signatures` | commit-gate | admin:dataset:manage | handleCreateSignature | provider |
| POST | `/api/v1/admin/token-metadata/backfill` | commit-gate | admin:dataset:manage | handleTokenBackfill | - |
| POST | `/api/v1/admin/tokens/{chain}/{contract}/override` | commit-gate | admin:dataset:manage | handleTokenOverride | provider |
| POST | `/api/v1/admin/tokens/{chain}/{contract}/resolve` | commit-gate | admin:dataset:manage | handleTokenResolve | provider |
| POST | `/api/v1/analysis/address-network` | actions-bearer |  | handleActionNetwork | clickhouse |
| POST | `/api/v1/analysis/addresses/{chain}/{address}/advanced/rebuild` | commit-gate | admin:dataset:manage | handleRebuildAddressAdvancedAnalysis | - |
| POST | `/api/v1/analysis/addresses/{chain}/{address}/semantic-events/rebuild` | commit-gate | admin:dataset:manage | handleRebuildAddressSemanticEvents | - |
| POST | `/api/v1/analysis/path` | actions-bearer |  | handleActionPath | clickhouse |
| POST | `/api/v1/api-control/alerts/{id}/actions` | commit-gate | admin:alert:manage | handleAPIControlAlerts | - |
| POST | `/api/v1/api-control/budgets` | commit-gate | admin:budget:update | handleAPIControlBudgets | - |
| PATCH | `/api/v1/api-control/control/kill-switch` | commit-gate | admin:killswitch:engage | handleAPIControlKillSwitch | - |
| POST | `/api/v1/api-control/control/overrides` | commit-gate | admin:performance:update | handleAPIControlOverride | - |
| PATCH | `/api/v1/api-control/control/policies` | commit-gate | admin:routing:update | handleAPIControlPolicies | - |
| DELETE | `/api/v1/api-control/provider-accounts/ankr` | commit-gate | admin:provider:update | handleAnkrManagementDisconnect | - |
| POST | `/api/v1/api-control/provider-accounts/ankr/connect` | commit-gate | admin:provider:update | handleAnkrManagementConnect | - |
| POST | `/api/v1/api-control/provider-accounts/ankr/sync` | commit-gate | admin:provider:update | handleAnkrManagementSync | - |
| PATCH | `/api/v1/canvases/{id}` | commit-gate | canvas:update | handleUpdateCanvas | - |
| POST | `/api/v1/canvases/{id}/branch` | commit-gate | canvas:branch | handleBranchCanvas | - |
| POST | `/api/v1/canvases/{id}/snapshots` | commit-gate | canvas:snapshot:create | handleCreateCanvasSnapshot | - |
| POST | `/api/v1/cases` | commit-gate | case:create | handleCreateCase | - |
| DELETE | `/api/v1/cases/{id}` | commit-gate | case:delete | handleDeleteCase | - |
| POST | `/api/v1/cases/{id}/canvases` | commit-gate | canvas:create | handleCreateCanvas | - |
| POST | `/api/v1/data-explorer/query` | commit-gate | explorer:read | handleExplorerQuery | - |
| POST | `/api/v1/data/ensure` | actions-bearer |  | handleActionEnsureData | - |
| POST | `/api/v1/datasets/coverage/preview` | commit-gate | explorer:read | handleCoveragePreview | - |
| POST | `/api/v1/datasets/ensure` | commit-gate | download:create | handleEnsure | - |
| POST | `/api/v1/download-jobs/{id}/cancel` | commit-gate | job:control | handleCancel | bus |
| POST | `/api/v1/download-jobs/{id}/guard/abort` | commit-gate | job:control | handleAbortVolumeGuard | bus |
| POST | `/api/v1/download-jobs/{id}/guard/continue` | commit-gate | job:control | handleContinueVolumeGuard | bus |
| POST | `/api/v1/download-jobs/{id}/pause` | commit-gate | job:control | handlePause | bus |
| POST | `/api/v1/download-jobs/{id}/performance-mode` | commit-gate | admin:performance:update | handleSetJobPerformanceMode | bus |
| POST | `/api/v1/download-jobs/{id}/resume` | commit-gate | job:control | handleResume | bus |
| POST | `/api/v1/download-jobs/{id}/subjects/{subjectID}/{action}` | commit-gate | job:control | handleSubjectControl | bus |
| POST | `/api/v1/export/create` | actions-bearer |  | handleActionCreateExport | clickhouse,object,provider |
| POST | `/api/v1/exports` | commit-gate | export:create | handleCreateExport | clickhouse,object,provider |
| POST | `/api/v1/exports/{id}/download-ticket` | commit-gate | export:download | handleExportTicket | - |
| POST | `/api/v1/investigations` | commit-gate | investigation:create | handleCreateInvestigation | - |
| POST | `/api/v1/investigations/{id}/relationships` | commit-gate | investigation:update | handleInvestigationRelationships | - |
| POST | `/api/v1/investigations/{id}/reports` | commit-gate | report:create | handleGenerateInvestigationReport | - |
| POST | `/api/v1/investigations/{id}/subjects` | commit-gate | subject:create | handleInvestigationSubjects | - |
| POST | `/api/v1/investigations/{id}/{action}` | commit-gate | investigation:run | handleInvestigationAction | - |
| POST | `/api/v1/providers/ankr/performance-mode` | commit-gate | admin:performance:update | handleSetAnkrPerformanceMode | - |
| POST | `/api/v1/tags` | commit-gate | case:update | handleCreateTag | - |
| DELETE | `/api/v1/tags/{id}` | commit-gate | case:update | handleDeleteTag | - |
| POST | `/api/v1/updates/preview` | commit-gate | admin:dataset:manage | handleUpdatePreview | - |
| POST | `/api/v1/updates/{id}/confirm` | commit-gate | admin:dataset:manage | handleUpdateConfirm | - |
| PATCH | `/api/v2/canvases/{id}` | commit-gate | canvas:update | handleCanvasV2Update | - |
| POST | `/api/v2/canvases/{id}/branches` | commit-gate | canvas:branch | handleCanvasV2Branch | - |
| POST | `/api/v2/canvases/{id}/snapshots` | commit-gate | canvas:snapshot:create | handleCanvasV2Snapshots | - |
| POST | `/api/v2/cases` | commit-gate | case:create | handleUserCreateCase | - |
| PATCH | `/api/v2/cases/{id}` | commit-gate | case:update | handleUserUpdateCase | - |
| POST | `/api/v2/cases/{id}/commands/{command}` | commit-gate | case:state:update | handleUserCaseCommand | - |
| POST | `/api/v2/cases/{id}/comments` | commit-gate | comment:create | handleUserCaseCommentAdd | - |
| POST | `/api/v2/cases/{id}/members` | commit-gate | case:member:manage | handleUserCaseMemberAdd | - |
| DELETE | `/api/v2/cases/{id}/members/{identity_id}` | commit-gate | case:member:manage | handleUserCaseMemberRemove | - |
| POST | `/api/v2/cases/{id}/reports` | commit-gate | report:create | handleUserCaseReportCreate | - |
| POST | `/api/v2/cases/{id}/subjects` | commit-gate | subject:create | handleUserCaseSubjectAdd | - |
| DELETE | `/api/v2/cases/{id}/subjects/{subject_id}` | commit-gate | subject:remove | handleUserCaseSubjectRemove | - |
| POST | `/api/v2/downloads` | commit-gate | download:create | handleDownloadCreate | clickhouse |
| POST | `/api/v2/downloads/preview` | commit-gate | download:create | handleDownloadPreview | clickhouse |
| POST | `/api/v2/downloads/validate-addresses` | commit-gate | download:create | handleDownloadValidateAddresses | provider |
| POST | `/api/v2/evidence/{id}/commands/dispute` | commit-gate | evidence:review | handleUserEvidenceDispute | - |
| POST | `/api/v2/evidence/{id}/commands/supersede` | commit-gate | evidence:review | handleUserEvidenceSupersede | - |
| POST | `/api/v2/evidence/{id}/commands/verify` | commit-gate | evidence:review | handleUserEvidenceVerify | - |
| POST | `/api/v2/exports` | commit-gate | export:create | handleExportV2Create | clickhouse,object,provider |
| POST | `/api/v2/exports/{id}/download-ticket` | commit-gate | export:download | handleExportV2Ticket | object |
| PATCH | `/api/v2/findings/{id}` | commit-gate | finding:update | handleUserFindingPatch | - |
| POST | `/api/v2/findings/{id}/commands/confirm` | commit-gate | finding:review | handleUserFindingConfirm | - |
| POST | `/api/v2/findings/{id}/commands/reject` | commit-gate | finding:review | handleUserFindingReject | - |
| POST | `/api/v2/findings/{id}/commands/supersede` | commit-gate | finding:review | handleUserFindingSupersede | - |
| POST | `/api/v2/investigations` | commit-gate | investigation:create | handleUserCreateInvestigation | - |
| POST | `/api/v2/investigations/preview` | commit-gate | investigation:create | handleUserInvestigationPreview | - |
| POST | `/api/v2/investigations/{id}/canvases` | commit-gate | canvas:create | handleInvestigationCanvasesV2 | - |
| POST | `/api/v2/investigations/{id}/commands/cancel` | commit-gate | investigation:cancel | handleUserInvestigationCancel | - |
| POST | `/api/v2/investigations/{id}/commands/pause` | commit-gate | investigation:pause | handleUserInvestigationPause | - |
| POST | `/api/v2/investigations/{id}/commands/resume` | commit-gate | investigation:resume | handleUserInvestigationResume | - |
| POST | `/api/v2/investigations/{id}/commands/retry` | commit-gate | investigation:retry | handleUserInvestigationRetry | - |
| POST | `/api/v2/investigations/{id}/commands/start` | commit-gate | investigation:start | handleUserInvestigationStart | - |
| POST | `/api/v2/investigations/{id}/commands/submit-review` | commit-gate | investigation:submit_review | handleUserInvestigationSubmitReview | - |
| POST | `/api/v2/jobs/{id}/commands/{command}` | commit-gate | job:control | handleUserJobCommand | - |
| PATCH | `/api/v2/me/preferences` | commit-gate | profile:update:self | handleUserPreferencesUpdate | - |
| PATCH | `/api/v2/me/profile` | commit-gate | profile:update:self | handleUserProfileUpdate | - |
| POST | `/api/v2/notifications/read-all` | commit-gate | notification:update | handleUserNotificationsReadAll | - |
| POST | `/api/v2/notifications/{id}/open` | commit-gate | notification:list | handleUserNotificationOpen | - |
| POST | `/api/v2/notifications/{id}/read` | commit-gate | notification:update | handleUserNotificationRead | - |
| POST | `/api/v2/reports/{id}/commands/approve` | commit-gate | report:review | handleUserReportApprove | - |
| POST | `/api/v2/reports/{id}/commands/archive` | commit-gate | report:review | handleUserReportArchive | - |
| POST | `/api/v2/reports/{id}/commands/reject` | commit-gate | report:review | handleUserReportReject | - |
| POST | `/api/v2/reports/{id}/commands/sign` | commit-gate | report:sign | handleUserReportSign | - |
| POST | `/api/v2/reports/{id}/commands/submit` | commit-gate | report:create | handleUserReportSubmit | - |
| POST | `/api/v2/reports/{id}/download-ticket` | commit-gate | report:download | handleUserReportTicket | - |
