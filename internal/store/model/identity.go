package model

import "time"

// AgentToken is the agent_tokens table.
type AgentToken struct {
	ID           UUID       `gorm:"column:id;primaryKey;not null;default:gen_random_uuid()"`
	AgentID      UUID       `gorm:"column:agent_id;not null;index:idx_agent_tokens_agent,priority:1"`
	Name         string     `gorm:"column:name;not null"`
	TokenHash    Text       `gorm:"column:token_hash;not null;size:128;uniqueIndex:agent_tokens_token_hash_key,priority:1"`
	Prefix       string     `gorm:"column:prefix;not null"`
	ExpiresAt    time.Time  `gorm:"column:expires_at;not null"`
	LastUsedAt   *time.Time `gorm:"column:last_used_at"`
	CreatedBy    UUID       `gorm:"column:created_by;not null"`
	CreatedAt    time.Time  `gorm:"column:created_at;not null;default:CURRENT_TIMESTAMP(3)"`
	RevokedAt    *time.Time `gorm:"column:revoked_at"`
	Agent        *Agent     `gorm:"foreignKey:AgentID;references:ID;constraint:OnDelete:CASCADE"`
	CreatedByRef *User      `gorm:"foreignKey:CreatedBy;references:ID;constraint:OnDelete:RESTRICT"`
}

func (AgentToken) TableName() string { return "agent_tokens" }

// AgentWorkspace is the agent_workspaces table.
type AgentWorkspace struct {
	AgentID     UUID       `gorm:"column:agent_id;primaryKey;not null"`
	WorkspaceID UUID       `gorm:"column:workspace_id;primaryKey;not null;index:idx_agent_workspaces_workspace,priority:1"`
	Agent       *Agent     `gorm:"foreignKey:AgentID;references:ID;constraint:OnDelete:CASCADE"`
	Workspace   *Workspace `gorm:"foreignKey:WorkspaceID;references:ID;constraint:OnDelete:CASCADE"`
}

func (AgentWorkspace) TableName() string { return "agent_workspaces" }

// Agent is the agents table.
type Agent struct {
	ID                 UUID      `gorm:"column:id;primaryKey;not null;default:gen_random_uuid();check:agents_kind_check,((kind = ANY (ARRAY['external'::text, 'assistant'::text])))"`
	OwnerID            UUID      `gorm:"column:owner_id;not null;index:idx_agents_owner,priority:1;check:agents_status_check,((status = ANY (ARRAY['active'::text, 'disabled'::text])))"`
	Kind               Text      `gorm:"column:kind;not null;size:128;default:'external';index:idx_agents_kind,priority:1"`
	Name               string    `gorm:"column:name;not null"`
	Description        Text      `gorm:"column:description;not null;size:1024;default:''"`
	Status             Text      `gorm:"column:status;not null;size:255;default:'active'"`
	CreatedAt          time.Time `gorm:"column:created_at;not null;default:CURRENT_TIMESTAMP(3)"`
	UpdatedAt          time.Time `gorm:"column:updated_at;not null;default:CURRENT_TIMESTAMP(3)"`
	Policy             JSON      `gorm:"column:policy;not null;default:('{}')"`
	PolicyUpdatedAt    time.Time `gorm:"column:policy_updated_at;not null;default:CURRENT_TIMESTAMP(3)"`
	PolicyUpdatedBy    *UUID     `gorm:"column:policy_updated_by"`
	Owner              *User     `gorm:"foreignKey:OwnerID;references:ID;constraint:OnDelete:CASCADE"`
	PolicyUpdatedByRef *User     `gorm:"foreignKey:PolicyUpdatedBy;references:ID;constraint:OnDelete:SET NULL"`
}

func (Agent) TableName() string { return "agents" }

// APIKey is the api_keys table.
type APIKey struct {
	ID         UUID        `gorm:"column:id;primaryKey;not null;default:gen_random_uuid()"`
	UserID     UUID        `gorm:"column:user_id;not null;index:idx_api_keys_user_id,priority:1"`
	Name       string      `gorm:"column:name;not null"`
	KeyHash    Text        `gorm:"column:key_hash;not null;size:128;uniqueIndex:api_keys_key_hash_key,priority:1;index:idx_api_keys_key_hash,priority:1"`
	ExpiresAt  *time.Time  `gorm:"column:expires_at"`
	CreatedAt  time.Time   `gorm:"column:created_at;not null;default:CURRENT_TIMESTAMP(3)"`
	Prefix     Text        `gorm:"column:prefix;not null;size:64;default:''"`
	Scopes     StringArray `gorm:"column:scopes;not null;default:'{*}'"`
	LastUsedAt *time.Time  `gorm:"column:last_used_at"`
	User       *User       `gorm:"foreignKey:UserID;references:ID;constraint:OnDelete:CASCADE"`
}

func (APIKey) TableName() string { return "api_keys" }

// AuthProvider is the auth_providers table.
type AuthProvider struct {
	ID        UUID      `gorm:"column:id;primaryKey;not null;default:gen_random_uuid();check:auth_providers_type_check,((type = ANY (ARRAY['oidc'::text, 'ldap'::text])))"`
	Name      string    `gorm:"column:name;not null"`
	Type      string    `gorm:"column:type;not null"`
	ConfigEnc string    `gorm:"column:config_enc;not null"`
	Enabled   bool      `gorm:"column:enabled;not null;default:true"`
	CreatedAt time.Time `gorm:"column:created_at;not null;default:CURRENT_TIMESTAMP(3)"`
	UpdatedAt time.Time `gorm:"column:updated_at;not null;default:CURRENT_TIMESTAMP(3)"`
}

func (AuthProvider) TableName() string { return "auth_providers" }

// OIDCLoginAttempt is the oidc_login_attempts table.
type OIDCLoginAttempt struct {
	State        Text          `gorm:"column:state;primaryKey;not null;size:128;check:oidc_login_attempts_redirect_check,(((redirect IS NULL) OR ((redirect ~~ '/%'::text) AND (redirect !~~ '//%'::text) AND (redirect !~ '\\\\'::text) AND (redirect !~ '[\\r\\n\\t]'::text))))"`
	ProviderID   UUID          `gorm:"column:provider_id;not null"`
	Nonce        *string       `gorm:"column:nonce"`
	CodeVerifier string        `gorm:"column:code_verifier;not null"`
	Redirect     *string       `gorm:"column:redirect"`
	ExpiresAt    time.Time     `gorm:"column:expires_at;not null;index:idx_oidc_login_attempts_expires_at,priority:1"`
	CreatedAt    time.Time     `gorm:"column:created_at;not null;default:CURRENT_TIMESTAMP(3)"`
	Provider     *AuthProvider `gorm:"foreignKey:ProviderID;references:ID;constraint:OnDelete:CASCADE"`
}

func (OIDCLoginAttempt) TableName() string { return "oidc_login_attempts" }

// PasswordResetToken is the password_reset_tokens table.
type PasswordResetToken struct {
	TokenHash Text       `gorm:"column:token_hash;primaryKey;not null;size:128"`
	UserID    UUID       `gorm:"column:user_id;not null;index:idx_password_reset_tokens_user_id,priority:1"`
	ExpiresAt time.Time  `gorm:"column:expires_at;not null;index:idx_password_reset_tokens_expires_at,priority:1"`
	UsedAt    *time.Time `gorm:"column:used_at"`
	CreatedAt time.Time  `gorm:"column:created_at;not null;default:CURRENT_TIMESTAMP(3)"`
	User      *User      `gorm:"foreignKey:UserID;references:ID;constraint:OnDelete:CASCADE"`
}

func (PasswordResetToken) TableName() string { return "password_reset_tokens" }

// RefreshToken is the refresh_tokens table.
type RefreshToken struct {
	ID        UUID      `gorm:"column:id;primaryKey;not null;default:gen_random_uuid()"`
	UserID    UUID      `gorm:"column:user_id;not null;index:idx_refresh_tokens_user_id,priority:1"`
	TokenHash Text      `gorm:"column:token_hash;not null;size:128;uniqueIndex:refresh_tokens_token_hash_key,priority:1"`
	ExpiresAt time.Time `gorm:"column:expires_at;not null;index:idx_refresh_tokens_expires_at,priority:1"`
	Revoked   bool      `gorm:"column:revoked;not null;default:false"`
	CreatedAt time.Time `gorm:"column:created_at;not null;default:CURRENT_TIMESTAMP(3)"`
	FamilyID  UUID      `gorm:"column:family_id;not null;index:idx_refresh_tokens_family_id,priority:1"`
	User      *User     `gorm:"foreignKey:UserID;references:ID;constraint:OnDelete:CASCADE"`
}

func (RefreshToken) TableName() string { return "refresh_tokens" }

// Team is the teams table.
type Team struct {
	ID           UUID      `gorm:"column:id;primaryKey;not null;default:gen_random_uuid()"`
	Name         string    `gorm:"column:name;not null"`
	Description  *string   `gorm:"column:description"`
	CreatedBy    UUID      `gorm:"column:created_by;not null;index:idx_teams_created_by,priority:1"`
	CreatedAt    time.Time `gorm:"column:created_at;not null;default:CURRENT_TIMESTAMP(3)"`
	UpdatedAt    time.Time `gorm:"column:updated_at;not null;default:CURRENT_TIMESTAMP(3)"`
	CreatedByRef *User     `gorm:"foreignKey:CreatedBy;references:ID;constraint:OnDelete:RESTRICT"`
}

func (Team) TableName() string { return "teams" }

// TeamMember is the team_members table.
type TeamMember struct {
	ID       UUID      `gorm:"column:id;primaryKey;not null;default:gen_random_uuid();check:team_members_role_check,((role = ANY (ARRAY['owner'::text, 'member'::text])))"`
	TeamID   UUID      `gorm:"column:team_id;not null;uniqueIndex:team_members_team_id_user_id_key,priority:1;index:idx_team_members_team_id,priority:1"`
	UserID   UUID      `gorm:"column:user_id;not null;uniqueIndex:team_members_team_id_user_id_key,priority:2;index:idx_team_members_user_id,priority:1"`
	Role     Text      `gorm:"column:role;not null;size:255;default:'member'"`
	JoinedAt time.Time `gorm:"column:joined_at;not null;default:CURRENT_TIMESTAMP(3)"`
	Team     *Team     `gorm:"foreignKey:TeamID;references:ID;constraint:OnDelete:CASCADE"`
	User     *User     `gorm:"foreignKey:UserID;references:ID;constraint:OnDelete:CASCADE"`
}

func (TeamMember) TableName() string { return "team_members" }

// UserIDPToken is the user_idp_tokens table.
type UserIDPToken struct {
	UserID          UUID          `gorm:"column:user_id;primaryKey;not null"`
	AuthProviderID  UUID          `gorm:"column:auth_provider_id;not null"`
	AccessTokenEnc  string        `gorm:"column:access_token_enc;not null"`
	RefreshTokenEnc *string       `gorm:"column:refresh_token_enc"`
	ExpiresAt       time.Time     `gorm:"column:expires_at;not null"`
	UpdatedAt       time.Time     `gorm:"column:updated_at;not null;default:CURRENT_TIMESTAMP(3)"`
	User            *User         `gorm:"foreignKey:UserID;references:ID;constraint:OnDelete:CASCADE"`
	AuthProvider    *AuthProvider `gorm:"foreignKey:AuthProviderID;references:ID;constraint:OnDelete:CASCADE"`
}

func (UserIDPToken) TableName() string { return "user_idp_tokens" }

// User is the users table.
type User struct {
	ID                  UUID          `gorm:"column:id;primaryKey;not null;default:gen_random_uuid();check:users_role_check,((role = ANY (ARRAY['admin'::text, 'member'::text])))"`
	Name                string        `gorm:"column:name;not null;check:chk_local_auth,(((auth_provider <> 'local'::text) OR (password_hash IS NOT NULL)))"`
	Email               Text          `gorm:"column:email;not null;size:320;uniqueIndex:users_email_key,priority:1;index:idx_users_email,priority:1;check:chk_oidc_auth,(((auth_provider <> 'oidc'::text) OR ((auth_provider_id IS NOT NULL) AND (external_id IS NOT NULL))))"`
	PasswordHash        *string       `gorm:"column:password_hash;check:chk_ldap_auth,(((auth_provider <> 'ldap'::text) OR ((auth_provider_id IS NOT NULL) AND (external_id IS NOT NULL))))"`
	Role                Text          `gorm:"column:role;not null;size:255;default:'member';check:users_status_check,((status = ANY (ARRAY['active'::text, 'suspended'::text])))"`
	AuthProvider        Text          `gorm:"column:auth_provider;not null;size:255;default:'local';check:users_locale_check,((locale = ANY (ARRAY['en'::text, 'pt-BR'::text])))"`
	AuthProviderID      *UUID         `gorm:"column:auth_provider_id;uniqueIndex:users_auth_provider_id_external_id_key,priority:1;index:idx_users_external_id,priority:1;check:users_kind_check,((kind = ANY (ARRAY['human'::text, 'service_account'::text])))"`
	ExternalID          *Text         `gorm:"column:external_id;size:255;uniqueIndex:users_auth_provider_id_external_id_key,priority:2;index:idx_users_external_id,priority:2;check:users_auth_provider_check,((auth_provider = ANY (ARRAY['local'::text, 'oidc'::text, 'ldap'::text, 'service'::text])))"`
	PersonalWorkspaceID *UUID         `gorm:"column:personal_workspace_id;check:chk_service_auth,(((auth_provider <> 'service'::text) OR (kind = 'service_account'::text)))"`
	CreatedAt           time.Time     `gorm:"column:created_at;not null;default:CURRENT_TIMESTAMP(3);check:chk_service_account,(((kind <> 'service_account'::text) OR ((role = 'member'::text) AND (password_hash IS NULL) AND (auth_provider = ANY (ARRAY['service'::text, 'oidc'::text])))))"`
	UpdatedAt           time.Time     `gorm:"column:updated_at;not null;default:CURRENT_TIMESTAMP(3)"`
	Status              Text          `gorm:"column:status;not null;size:255;default:'active'"`
	SuspendedAt         *time.Time    `gorm:"column:suspended_at"`
	SuspendedReason     *string       `gorm:"column:suspended_reason"`
	DirectorySyncedAt   *time.Time    `gorm:"column:directory_synced_at"`
	Locale              *string       `gorm:"column:locale"`
	Kind                Text          `gorm:"column:kind;not null;size:255;default:'human'"`
	HomeWorkspaceID     *UUID         `gorm:"column:home_workspace_id"`
	Description         *string       `gorm:"column:description"`
	AuthProviderRef     *AuthProvider `gorm:"foreignKey:AuthProviderID;references:ID;constraint:OnDelete:SET NULL"`
	PersonalWorkspace   *Workspace    `gorm:"foreignKey:PersonalWorkspaceID;references:ID;constraint:OnDelete:SET NULL"`
	HomeWorkspace       *Workspace    `gorm:"foreignKey:HomeWorkspaceID;references:ID;constraint:OnDelete:SET NULL"`
}

func (User) TableName() string { return "users" }

// WorkspaceMember is the workspace_members table.
type WorkspaceMember struct {
	WorkspaceID  UUID       `gorm:"column:workspace_id;primaryKey;not null;check:workspace_members_role_check,((role = ANY (ARRAY['admin'::text, 'editor'::text, 'viewer'::text])))"`
	UserID       UUID       `gorm:"column:user_id;primaryKey;not null;index:workspace_members_user,priority:1;check:workspace_members_source_check,((source = ANY (ARRAY['manual'::text, 'idp_group'::text])))"`
	Role         string     `gorm:"column:role;not null"`
	GrantedBy    *UUID      `gorm:"column:granted_by"`
	GrantedAt    time.Time  `gorm:"column:granted_at;not null;default:CURRENT_TIMESTAMP(3)"`
	Source       Text       `gorm:"column:source;not null;size:255;default:'manual'"`
	Workspace    *Workspace `gorm:"foreignKey:WorkspaceID;references:ID;constraint:OnDelete:CASCADE"`
	User         *User      `gorm:"foreignKey:UserID;references:ID;constraint:OnDelete:CASCADE"`
	GrantedByRef *User      `gorm:"foreignKey:GrantedBy;references:ID;constraint:OnDelete:SET NULL"`
}

func (WorkspaceMember) TableName() string { return "workspace_members" }

// Workspace is the workspaces table.
type Workspace struct {
	ID                 UUID            `gorm:"column:id;primaryKey;not null;default:gen_random_uuid();check:workspaces_type_check,((type = ANY (ARRAY['personal'::text, 'shared'::text])))"`
	Name               string          `gorm:"column:name;not null;check:chk_workspace_owner,((((type = 'personal'::text) AND (owner_user_id IS NOT NULL) AND (owner_team_id IS NULL)) OR ((type = 'shared'::text) AND (owner_team_id IS NOT NULL) AND (owner_user_id IS NULL))))"`
	Description        *string         `gorm:"column:description"`
	Type               string          `gorm:"column:type;not null"`
	PolarisCatalogName Text            `gorm:"column:polaris_catalog_name;not null;size:128;uniqueIndex:workspaces_polaris_catalog_name_key,priority:1"`
	OwnerUserID        *UUID           `gorm:"column:owner_user_id;index:idx_workspaces_owner_user,priority:1"`
	OwnerTeamID        *UUID           `gorm:"column:owner_team_id;index:idx_workspaces_owner_team,priority:1"`
	CreatedAt          time.Time       `gorm:"column:created_at;not null;default:CURRENT_TIMESTAMP(3)"`
	UpdatedAt          time.Time       `gorm:"column:updated_at;not null;default:CURRENT_TIMESTAMP(3)"`
	DefaultBindingID   *UUID           `gorm:"column:default_binding_id"`
	DefaultSchema      *string         `gorm:"column:default_schema"`
	OwnerUser          *User           `gorm:"foreignKey:OwnerUserID;references:ID;constraint:OnDelete:RESTRICT"`
	OwnerTeam          *Team           `gorm:"foreignKey:OwnerTeamID;references:ID;constraint:OnDelete:RESTRICT"`
	DefaultBinding     *CatalogBinding `gorm:"foreignKey:DefaultBindingID;references:ID;constraint:OnDelete:SET NULL"`
}

func (Workspace) TableName() string { return "workspaces" }
