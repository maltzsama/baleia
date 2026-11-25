// The people, teams and service accounts a policy can name (#1146). Each list
// is best-effort: listing users is for global admins, so a connection owner
// sees ids where names are not readable, and still edits by id.
import { client } from '$lib/api/client';
import { m } from '$lib/paraglide/messages';
import type { DataPolicySubject } from './api';

export type DirectoryEntry = { id: string; name: string; detail?: string };

export type Directory = {
	users: DirectoryEntry[];
	teams: DirectoryEntry[];
	serviceAccounts: DirectoryEntry[];
};

export const emptyDirectory: Directory = { users: [], teams: [], serviceAccounts: [] };

async function users(): Promise<DirectoryEntry[]> {
	const out: DirectoryEntry[] = [];
	for (let page = 1; page <= 20; page++) {
		const { data, error } = await client.GET('/users', {
			params: { query: { page, per_page: 100 } }
		});
		if (error || !data?.data) break;
		out.push(
			...data.data
				.filter((user) => user.kind !== 'service_account')
				.map((user) => ({ id: user.id, name: user.name, detail: user.email }))
		);
		const total = data.pagination?.total_pages ?? 1;
		if (page >= total) break;
	}
	return out;
}

async function teams(): Promise<DirectoryEntry[]> {
	const { data, error } = await client.GET('/teams');
	if (error || !data) return [];
	return data.map((team) => ({ id: team.id, name: team.name }));
}

async function serviceAccounts(): Promise<DirectoryEntry[]> {
	const { data, error } = await client.GET('/service-accounts');
	if (error || !data) return [];
	return data.map((account) => ({ id: account.id, name: account.name, detail: account.email }));
}

/** Loads what the caller may read; a list it may not read is empty. */
export async function loadDirectory(): Promise<Directory> {
	const [u, t, s] = await Promise.all([users(), teams(), serviceAccounts()]);
	return { users: u, teams: t, serviceAccounts: s };
}

function nameOf(list: DirectoryEntry[], id: string): string {
	return list.find((entry) => entry.id === id)?.name ?? id;
}

/** A readable label of one subject reference, without its exceptions. */
export function subjectLabel(subject: DataPolicySubject, directory: Directory): string {
	if (subject.everyone) return m.governance_subject_everyone();
	if (subject.user)
		return m.governance_subject_user({ name: nameOf(directory.users, subject.user) });
	if (subject.team)
		return m.governance_subject_team({ name: nameOf(directory.teams, subject.team) });
	if (subject.service_account) {
		return m.governance_subject_service_account({
			name: nameOf(directory.serviceAccounts, subject.service_account)
		});
	}
	return m.governance_subject_group({ name: subject.group ?? '' });
}

/** A subject reference with its exceptions spelled out. */
export function subjectSummary(subject: DataPolicySubject, directory: Directory): string {
	const base = subjectLabel(subject, directory);
	const except = subject.except ?? [];
	if (!subject.everyone || except.length === 0) return base;
	return m.governance_subject_everyone_except({
		names: except.map((item) => subjectLabel(item, directory)).join(', ')
	});
}
