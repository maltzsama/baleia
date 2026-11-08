package model

import "time"

// User is a human user or a service account (kind distinguishes them).
type User struct {
	ID                  UUID       `gorm:"column:id;primaryKey;default:gen_random_uuid()"`
	Name                string     `gorm:"column:name;not null"`
	Email               Text       `gorm:"column:email;not null;size:320;uniqueIndex:users_email_key"`
	PasswordHash        *string    `gorm:"column:password_hash;check:chk_local_auth,auth_provider <> 'local' OR password_hash IS NOT NULL"`
	Role                Text       `gorm:"column:role;not null;size:32;default:'member';check:users_role_check,role IN ('admin','member')"`
	AuthProvider        Text       `gorm:"column:auth_provider;not null;size:32;default:'local';check:users_auth_provider_check,auth_provider IN ('local','oidc','ldap','service')"`
	AuthProviderID      *UUID      `gorm:"column:auth_provider_id;uniqueIndex:users_auth_provider_id_external_id_key,priority:1;check:chk_ldap_auth,auth_provider <> 'ldap' OR (auth_provider_id IS NOT NULL AND external_id IS NOT NULL)"`
	ExternalID          *Text      `gorm:"column:external_id;size:255;uniqueIndex:users_auth_provider_id_external_id_key,priority:2;check:chk_oidc_auth,auth_provider <> 'oidc' OR (auth_provider_id IS NOT NULL AND external_id IS NOT NULL)"`
	PersonalWorkspaceID *UUID      `gorm:"column:personal_workspace_id"`
	CreatedAt           time.Time  `gorm:"column:created_at;not null;default:CURRENT_TIMESTAMP(3)"`
	UpdatedAt           time.Time  `gorm:"column:updated_at;not null;default:CURRENT_TIMESTAMP(3)"`
	Status              Text       `gorm:"column:status;not null;size:32;default:'active';check:users_status_check,status IN ('active','suspended')"`
	SuspendedAt         *time.Time `gorm:"column:suspended_at"`
	SuspendedReason     *string    `gorm:"column:suspended_reason"`
	DirectorySyncedAt   *time.Time `gorm:"column:directory_synced_at"`
	Locale              *string    `gorm:"column:locale;check:users_locale_check,locale IN ('en','pt-BR')"`
	Kind                Text       `gorm:"column:kind;not null;size:32;default:'human';check:users_kind_check,kind IN ('human','service_account')"`
	HomeWorkspaceID     *UUID      `gorm:"column:home_workspace_id;check:chk_service_auth,auth_provider <> 'service' OR kind = 'service_account'"`
	Description         *string    `gorm:"column:description;check:chk_service_account,kind <> 'service_account' OR (role = 'member' AND password_hash IS NULL AND auth_provider IN ('service','oidc'))"`
}

func (User) TableName() string { return "users" }

// APIKey authenticates a user against the public API.
type APIKey struct {
	ID         UUID        `gorm:"column:id;primaryKey;default:gen_random_uuid()"`
	UserID     UUID        `gorm:"column:user_id;not null;index:idx_api_keys_user_id"`
	Name       string      `gorm:"column:name;not null"`
	KeyHash    Text        `gorm:"column:key_hash;not null;size:128;uniqueIndex:api_keys_key_hash_key;index:idx_api_keys_key_hash"`
	ExpiresAt  *time.Time  `gorm:"column:expires_at"`
	CreatedAt  time.Time   `gorm:"column:created_at;not null;default:CURRENT_TIMESTAMP(3)"`
	Prefix     Text        `gorm:"column:prefix;not null;size:64;default:''"`
	Scopes     StringArray `gorm:"column:scopes;not null;default:'{*}'"`
	LastUsedAt *time.Time  `gorm:"column:last_used_at"`
}

func (APIKey) TableName() string { return "api_keys" }

// AuthProvider is an OIDC or LDAP identity provider.
type AuthProvider struct {
	ID        UUID      `gorm:"column:id;primaryKey;default:gen_random_uuid()"`
	Name      string    `gorm:"column:name;not null"`
	Type      string    `gorm:"column:type;not null;check:auth_providers_type_check,type IN ('oidc','ldap')"`
	ConfigEnc string    `gorm:"column:config_enc;not null"`
	Enabled   bool      `gorm:"column:enabled;not null;default:true"`
	CreatedAt time.Time `gorm:"column:created_at;not null;default:CURRENT_TIMESTAMP(3)"`
	UpdatedAt time.Time `gorm:"column:updated_at;not null;default:CURRENT_TIMESTAMP(3)"`
}

func (AuthProvider) TableName() string { return "auth_providers" }

// RefreshToken is one rotation member of a refresh-token family.
type RefreshToken struct {
	ID        UUID      `gorm:"column:id;primaryKey;default:gen_random_uuid()"`
	UserID    UUID      `gorm:"column:user_id;not null;index:idx_refresh_tokens_user_id"`
	TokenHash Text      `gorm:"column:token_hash;not null;size:128;uniqueIndex:refresh_tokens_token_hash_key"`
	ExpiresAt time.Time `gorm:"column:expires_at;not null;index:idx_refresh_tokens_expires_at"`
	Revoked   bool      `gorm:"column:revoked;not null;default:false"`
	CreatedAt time.Time `gorm:"column:created_at;not null;default:CURRENT_TIMESTAMP(3)"`
	FamilyID  UUID      `gorm:"column:family_id;not null;index:idx_refresh_tokens_family_id"`
}

func (RefreshToken) TableName() string { return "refresh_tokens" }

// PasswordResetToken is a single-use password reset grant.
type PasswordResetToken struct {
	TokenHash Text       `gorm:"column:token_hash;primaryKey;size:128"`
	UserID    UUID       `gorm:"column:user_id;not null;index:idx_password_reset_tokens_user_id"`
	ExpiresAt time.Time  `gorm:"column:expires_at;not null;index:idx_password_reset_tokens_expires_at"`
	UsedAt    *time.Time `gorm:"column:used_at"`
	CreatedAt time.Time  `gorm:"column:created_at;not null;default:CURRENT_TIMESTAMP(3)"`
}

func (PasswordResetToken) TableName() string { return "password_reset_tokens" }

// OIDCLoginAttempt is a short-lived state for an in-flight OIDC login.
type OIDCLoginAttempt struct {
	State        Text      `gorm:"column:state;primaryKey;size:128"`
	ProviderID   UUID      `gorm:"column:provider_id;not null"`
	Nonce        *string   `gorm:"column:nonce"`
	CodeVerifier string    `gorm:"column:code_verifier;not null"`
	Redirect     *string   `gorm:"column:redirect"`
	ExpiresAt    time.Time `gorm:"column:expires_at;not null;index:idx_oidc_login_attempts_expires_at"`
	CreatedAt    time.Time `gorm:"column:created_at;not null;default:CURRENT_TIMESTAMP(3)"`
}

func (OIDCLoginAttempt) TableName() string { return "oidc_login_attempts" }

// UserIDPToken is the sealed token pair from an OIDC provider.
type UserIDPToken struct {
	UserID          UUID      `gorm:"column:user_id;primaryKey"`
	AuthProviderID  UUID      `gorm:"column:auth_provider_id;not null"`
	AccessTokenEnc  string    `gorm:"column:access_token_enc;not null"`
	RefreshTokenEnc *string   `gorm:"column:refresh_token_enc"`
	ExpiresAt       time.Time `gorm:"column:expires_at;not null"`
	UpdatedAt       time.Time `gorm:"column:updated_at;not null;default:CURRENT_TIMESTAMP(3)"`
}

func (UserIDPToken) TableName() string { return "user_idp_tokens" }

// Team owns shared workspaces.
type Team struct {
	ID          UUID      `gorm:"column:id;primaryKey;default:gen_random_uuid()"`
	Name        string    `gorm:"column:name;not null"`
	Description *string   `gorm:"column:description"`
	CreatedBy   UUID      `gorm:"column:created_by;not null;index:idx_teams_created_by"`
	CreatedAt   time.Time `gorm:"column:created_at;not null;default:CURRENT_TIMESTAMP(3)"`
	UpdatedAt   time.Time `gorm:"column:updated_at;not null;default:CURRENT_TIMESTAMP(3)"`
}

func (Team) TableName() string { return "teams" }

// TeamMember links a user to a team.
type TeamMember struct {
	ID       UUID      `gorm:"column:id;primaryKey;default:gen_random_uuid()"`
	TeamID   UUID      `gorm:"column:team_id;not null;uniqueIndex:team_members_team_id_user_id_key,priority:1;index:idx_team_members_team_id"`
	UserID   UUID      `gorm:"column:user_id;not null;uniqueIndex:team_members_team_id_user_id_key,priority:2;index:idx_team_members_user_id"`
	Role     Text      `gorm:"column:role;not null;size:32;default:'member';check:team_members_role_check,role IN ('owner','member')"`
	JoinedAt time.Time `gorm:"column:joined_at;not null;default:CURRENT_TIMESTAMP(3)"`
}

func (TeamMember) TableName() string { return "team_members" }

// Workspace is a personal or team-owned namespace.
type Workspace struct {
	ID                 UUID      `gorm:"column:id;primaryKey;default:gen_random_uuid()"`
	Name               string    `gorm:"column:name;not null"`
	Description        *string   `gorm:"column:description"`
	Type               string    `gorm:"column:type;not null;check:workspaces_type_check,type IN ('personal','shared')"`
	PolarisCatalogName Text      `gorm:"column:polaris_catalog_name;not null;size:255;uniqueIndex:workspaces_polaris_catalog_name_key"`
	OwnerUserID        *UUID     `gorm:"column:owner_user_id;index:idx_workspaces_owner_user;check:chk_workspace_owner,(type = 'personal' AND owner_user_id IS NOT NULL AND owner_team_id IS NULL) OR (type = 'shared' AND owner_team_id IS NOT NULL AND owner_user_id IS NULL)"`
	OwnerTeamID        *UUID     `gorm:"column:owner_team_id;index:idx_workspaces_owner_team"`
	CreatedAt          time.Time `gorm:"column:created_at;not null;default:CURRENT_TIMESTAMP(3)"`
	UpdatedAt          time.Time `gorm:"column:updated_at;not null;default:CURRENT_TIMESTAMP(3)"`
	DefaultBindingID   *UUID     `gorm:"column:default_binding_id"`
	DefaultSchema      *string   `gorm:"column:default_schema"`
}

func (Workspace) TableName() string { return "workspaces" }

// WorkspaceMember grants a user a role in a workspace.
type WorkspaceMember struct {
	WorkspaceID UUID      `gorm:"column:workspace_id;primaryKey"`
	UserID      UUID      `gorm:"column:user_id;primaryKey;index:workspace_members_user"`
	Role        string    `gorm:"column:role;not null;check:workspace_members_role_check,role IN ('admin','editor','viewer')"`
	GrantedBy   *UUID     `gorm:"column:granted_by"`
	GrantedAt   time.Time `gorm:"column:granted_at;not null;default:CURRENT_TIMESTAMP(3)"`
	Source      Text      `gorm:"column:source;not null;size:32;default:'manual';check:workspace_members_source_check,source IN ('manual','idp_group')"`
}

func (WorkspaceMember) TableName() string { return "workspace_members" }

// Agent is an external or assistant agent owned by a user.
type Agent struct {
	ID              UUID      `gorm:"column:id;primaryKey;default:gen_random_uuid()"`
	OwnerID         UUID      `gorm:"column:owner_id;not null;index:idx_agents_owner"`
	Kind            Text      `gorm:"column:kind;not null;size:32;default:'external';check:agents_kind_check,kind IN ('external','assistant');index:idx_agents_kind"`
	Name            string    `gorm:"column:name;not null"`
	Description     Text      `gorm:"column:description;not null;size:1024;default:''"`
	Status          Text      `gorm:"column:status;not null;size:32;default:'active';check:agents_status_check,status IN ('active','disabled')"`
	CreatedAt       time.Time `gorm:"column:created_at;not null;default:CURRENT_TIMESTAMP(3)"`
	UpdatedAt       time.Time `gorm:"column:updated_at;not null;default:CURRENT_TIMESTAMP(3)"`
	Policy          JSON      `gorm:"column:policy;not null;default:('{}')"`
	PolicyUpdatedAt time.Time `gorm:"column:policy_updated_at;not null;default:CURRENT_TIMESTAMP(3)"`
	PolicyUpdatedBy *UUID     `gorm:"column:policy_updated_by"`
}

func (Agent) TableName() string { return "agents" }

// AgentToken authenticates an agent.
type AgentToken struct {
	ID         UUID       `gorm:"column:id;primaryKey;default:gen_random_uuid()"`
	AgentID    UUID       `gorm:"column:agent_id;not null;index:idx_agent_tokens_agent"`
	Name       string     `gorm:"column:name;not null"`
	TokenHash  Text       `gorm:"column:token_hash;not null;size:128;uniqueIndex:agent_tokens_token_hash_key"`
	Prefix     string     `gorm:"column:prefix;not null"`
	ExpiresAt  time.Time  `gorm:"column:expires_at;not null"`
	LastUsedAt *time.Time `gorm:"column:last_used_at"`
	CreatedBy  UUID       `gorm:"column:created_by;not null"`
	CreatedAt  time.Time  `gorm:"column:created_at;not null;default:CURRENT_TIMESTAMP(3)"`
	RevokedAt  *time.Time `gorm:"column:revoked_at"`
}

func (AgentToken) TableName() string { return "agent_tokens" }

// AgentWorkspace grants an agent access to a workspace.
type AgentWorkspace struct {
	AgentID     UUID `gorm:"column:agent_id;primaryKey"`
	WorkspaceID UUID `gorm:"column:workspace_id;primaryKey;index:idx_agent_workspaces_workspace"`
}

func (AgentWorkspace) TableName() string { return "agent_workspaces" }
