package audit

// Action is one entry of the bounded audit vocabulary: <resource>.<verb>.
// Adding an action means adding it here and to allActions
// (TestActionsAreBounded).
type Action string

const (
	ConnectionCreate Action = "connection.create"
	ConnectionUpdate Action = "connection.update"
	ConnectionDelete Action = "connection.delete"
	ConnectionTest   Action = "connection.test"

	CatalogCreate    Action = "catalog.create"
	CatalogUpdate    Action = "catalog.update"
	CatalogRename    Action = "catalog.rename"
	CatalogDelete    Action = "catalog.delete"
	CatalogSync      Action = "catalog.sync"
	CatalogReconcile Action = "catalog.reconcile"

	BindingCreate Action = "binding.create"
	BindingUpdate Action = "binding.update"
	BindingDelete Action = "binding.delete"
	BindingSync   Action = "binding.sync"

	GrantCreate Action = "grant.create"
	GrantRevoke Action = "grant.revoke"

	WorkspaceUpdate      Action = "workspace.update"
	WorkspaceDelete      Action = "workspace.delete"
	SemanticDocCertify   Action = "semantic_doc.certify"
	SemanticDocUncertify Action = "semantic_doc.uncertify"

	TeamMemberAdd    Action = "team.member_add"
	TeamMemberRemove Action = "team.member_remove"
	TeamRoleChange   Action = "team.role_change"

	UserInvite     Action = "user.invite"
	UserRoleChange Action = "user.role_change"
	UserDisable    Action = "user.disable"
	UserSuspend    Action = "user.suspend"
	UserReinstate  Action = "user.reinstate"

	IdentityProviderCreate Action = "identity_provider.create"
	IdentityProviderUpdate Action = "identity_provider.update"
	IdentityProviderDelete Action = "identity_provider.delete"

	AuthLogin                Action = "auth.login"
	AuthLogout               Action = "auth.logout"
	AuthRefresh              Action = "auth.refresh"
	AuthPasswordResetRequest Action = "auth.password_reset_request"
	AuthPasswordResetConfirm Action = "auth.password_reset_confirm"

	APIKeyCreate Action = "api_key.create"
	APIKeyRevoke Action = "api_key.revoke"

	AgentCreate            Action = "agent.create"
	AgentUpdate            Action = "agent.update"
	AgentDisable           Action = "agent.disable"
	AgentDelete            Action = "agent.delete"
	AgentTokenCreate       Action = "agent_token.create"
	AgentTokenRevoke       Action = "agent_token.revoke"
	AgentToolCall          Action = "agent.tool_call"
	AgentPolicyUpdate      Action = "agent_policy.update"
	AuthAgentTokenRejected Action = "auth.agent_token_rejected"

	ExecutionCreate Action = "execution.create"
	ExecutionCancel Action = "execution.cancel"

	ResultsDownload Action = "results.download"

	MaintenanceTrigger Action = "maintenance.trigger"

	RetrieverCreate  Action = "retriever.create"
	RetrieverUpdate  Action = "retriever.update"
	RetrieverDelete  Action = "retriever.delete"
	RetrieverRefresh Action = "retriever.refresh"

	MetadataSync Action = "metadata.sync"

	SpaceCreate       Action = "space.create"
	SpaceUpdate       Action = "space.update"
	SpaceDelete       Action = "space.delete"
	SpaceMemberSet    Action = "space.member_set"
	SpaceMemberRemove Action = "space.member_remove"

	SavedQueryCreate Action = "saved_query.create"
	SavedQueryUpdate Action = "saved_query.update"
	SavedQueryDelete Action = "saved_query.delete"
	FolderCreate     Action = "folder.create"
	FolderDelete     Action = "folder.delete"
)

// allActions is the whole vocabulary.
var allActions = []Action{
	ConnectionCreate, ConnectionUpdate, ConnectionDelete, ConnectionTest,
	CatalogCreate, CatalogUpdate, CatalogRename, CatalogDelete, CatalogSync, CatalogReconcile,
	BindingCreate, BindingUpdate, BindingDelete, BindingSync,
	GrantCreate, GrantRevoke,
	WorkspaceUpdate,
	TeamMemberAdd, TeamMemberRemove, TeamRoleChange,
	UserInvite, UserRoleChange, UserDisable, UserSuspend, UserReinstate,
	IdentityProviderCreate, IdentityProviderUpdate, IdentityProviderDelete,
	AuthLogin, AuthLogout, AuthRefresh, AuthPasswordResetRequest, AuthPasswordResetConfirm,
	APIKeyCreate, APIKeyRevoke,
	AgentCreate, AgentUpdate, AgentDisable, AgentDelete,
	AgentTokenCreate, AgentTokenRevoke, AuthAgentTokenRejected,
	AgentToolCall, AgentPolicyUpdate,
	ExecutionCreate, ExecutionCancel,
	ResultsDownload,
	MaintenanceTrigger,
	RetrieverCreate, RetrieverUpdate, RetrieverDelete, RetrieverRefresh,
	MetadataSync,
	SpaceCreate, SpaceUpdate, SpaceDelete, SpaceMemberSet, SpaceMemberRemove,
	SavedQueryCreate, SavedQueryUpdate, SavedQueryDelete, FolderCreate, FolderDelete,
}

// Actions returns the vocabulary.
func Actions() []Action { return append([]Action(nil), allActions...) }

// Known reports whether a is in the vocabulary.
func Known(a Action) bool {
	for _, k := range allActions {
		if k == a {
			return true
		}
	}
	return false
}
