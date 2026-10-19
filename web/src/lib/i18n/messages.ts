// Typed message catalog (#67). `ptBR` is a Record<MessageKey, string>, so a
// missing translation is a TypeScript error; `en` is the source of truth.
export const en = {
	app_name: 'Baleia',
	nav_sql: 'SQL',
	nav_data: 'Data',
	nav_connections: 'Connections',
	nav_monitoring: 'Monitoring',
	nav_retrievers: 'Retrievers',
	nav_history: 'History',
	nav_settings: 'Settings',
	nav_profile: 'Profile',
	nav_signout: 'Sign out',
	profile_title: 'Profile',
	profile_language: 'Language',
	profile_language_auto: 'Follow the browser',
	profile_save: 'Save profile',
	error_unauthorized: 'Your session expired. Sign in again.',
	error_forbidden: 'You are not allowed to do that.',
	error_not_found: 'Not found.',
	error_conflict: 'Someone else changed this first. Reload and try again.',
	error_validation_error: 'Check the highlighted fields.',
	error_rate_limited: 'Too many attempts. Try again shortly.',
	error_connection_error: 'The source could not be reached.',
	error_engine_error: 'The engine rejected the operation.',
	error_service_unavailable: 'The service is temporarily unavailable.',
	error_bad_request: 'The request was invalid.',
	error_internal: 'Something went wrong on the server.'
} as const;

export type MessageKey = keyof typeof en;

export const ptBR: Record<MessageKey, string> = {
	app_name: 'Baleia',
	nav_sql: 'SQL',
	nav_data: 'Dados',
	nav_connections: 'Conexões',
	nav_monitoring: 'Monitoramento',
	nav_retrievers: 'Recuperadores',
	nav_history: 'Histórico',
	nav_settings: 'Configurações',
	nav_profile: 'Perfil',
	nav_signout: 'Sair',
	profile_title: 'Perfil',
	profile_language: 'Idioma',
	profile_language_auto: 'Seguir o navegador',
	profile_save: 'Salvar perfil',
	error_unauthorized: 'Sua sessão expirou. Entre novamente.',
	error_forbidden: 'Você não tem permissão para fazer isso.',
	error_not_found: 'Não encontrado.',
	error_conflict: 'Outra pessoa alterou isto primeiro. Recarregue e tente de novo.',
	error_validation_error: 'Verifique os campos destacados.',
	error_rate_limited: 'Muitas tentativas. Tente novamente em instantes.',
	error_connection_error: 'Não foi possível alcançar a origem.',
	error_engine_error: 'O motor recusou a operação.',
	error_service_unavailable: 'O serviço está temporariamente indisponível.',
	error_bad_request: 'A requisição é inválida.',
	error_internal: 'Algo deu errado no servidor.'
};
