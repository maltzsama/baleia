<script lang="ts">
	import { m } from '$lib/paraglide/messages';
	import { apiErrorMessage, client } from '$lib/api/client';
	import { localeStore } from '$lib/i18n/locale.svelte';
	import { authStore, type AuthUser } from '$lib/stores/auth.svelte';
	import { toasts } from '$lib/ui/toast.svelte';
	import Button from '$lib/ui/Button.svelte';
	import Input from '$lib/ui/Input.svelte';
	import PasswordInput from '$lib/ui/PasswordInput.svelte';
	import Select from '$lib/ui/Select.svelte';
	import Skeleton from '$lib/ui/Skeleton.svelte';

	const languageOptions = [
		{ value: 'en', label: m.settings_profile_english() },
		{ value: 'pt-BR', label: m.settings_profile_portugues_brasil() }
	];

	const user = $derived(authStore.user);

	let saving = $state(false);
	let changingPassword = $state(false);
	let error = $state('');

	// On a cold load the page mounts before /auth/me resolves, so the fields
	// start from the user already present (a client-side navigation) and
	// follow the user once it arrives, without clobbering later edits (#1078).
	let syncedUserId = authStore.user?.id ?? null;
	let name = $state(authStore.user?.name ?? '');
	let email = $state(authStore.user?.email ?? '');
	let locale = $state(authStore.user?.locale ?? localeStore.current);

	let currentPassword = $state('');
	let newPassword = $state('');
	let confirmPassword = $state('');

	$effect(() => {
		const loaded = user;
		if (!loaded || loaded.id === syncedUserId) return;
		syncedUserId = loaded.id;
		name = loaded.name;
		email = loaded.email;
		locale = loaded.locale ?? localeStore.current;
	});

	async function saveProfile() {
		saving = true;
		error = '';
		const { data, error: err } = await client.PATCH('/me', {
			body: { name, email, locale: locale as 'en' | 'pt-BR' }
		});
		saving = false;
		if (data) {
			authStore.set(data as AuthUser);
			toasts.success(m.settings_profile_profile_updated());
		} else {
			error = apiErrorMessage(err, m.settings_profile_failed_to_save_profile());
		}
	}

	async function changePassword() {
		if (newPassword !== confirmPassword) {
			error = m.settings_profile_passwords_do_not_match();
			return;
		}
		changingPassword = true;
		error = '';
		const { response, error: err } = await client.PATCH('/me/password', {
			body: { current_password: currentPassword, new_password: newPassword }
		});
		changingPassword = false;
		if (response.ok) {
			toasts.success(m.settings_profile_password_changed());
			currentPassword = '';
			newPassword = '';
			confirmPassword = '';
		} else {
			error = apiErrorMessage(err, m.settings_profile_failed_to_change_password());
		}
	}
</script>

<div class="flex-1 overflow-y-auto p-7">
	<div class="max-w-3xl">
		<h1 class="text-xl font-semibold tracking-[-0.3px]">{m.nav_profile()}</h1>
		<p class="mt-1 text-[13px] text-text-muted">
			{m.settings_profile_your_name_email_language_and_password()}
		</p>

		{#if user}
			<div class="mt-6 flex flex-col gap-4">
				{#if error}
					<div
						role="alert"
						class="rounded-md border border-danger/40 bg-danger/5 px-3 py-2 text-[12px] text-danger"
					>
						{error}
					</div>
				{/if}

				<section class="rounded-lg border border-border bg-bg-panel p-4">
					<h2 class="text-[13px] font-semibold">{m.settings_profile_personal_info()}</h2>
					<div class="mt-3 flex items-center gap-3">
						<div
							class="flex h-11 w-11 shrink-0 items-center justify-center rounded-full bg-primary-solid text-base font-semibold text-white"
						>
							{authStore.initials}
						</div>
						<div class="min-w-0">
							<div class="truncate text-[13px] font-semibold text-text-main">{user.name}</div>
							<div class="truncate text-[12px] text-text-muted">
								{user.email} &middot; {user.role}
							</div>
						</div>
					</div>
					<div class="mt-4 grid gap-3 sm:grid-cols-2">
						<Input label={m.common_name()} autocomplete="name" bind:value={name} />
						<Input
							label={m.common_email()}
							type="email"
							autocomplete="email"
							bind:value={email}
							disabled={user.auth_provider !== 'local'}
						/>
					</div>
					<div class="mt-3 sm:max-w-xs">
						<Select
							label={m.settings_profile_interface_language()}
							bind:value={locale}
							options={languageOptions}
						/>
					</div>
					<p class="mt-1.5 text-[11px] text-text-muted">
						{m.settings_profile_the_change_applies_after_saving_and_follow()}
					</p>
					<div class="mt-4 flex justify-end">
						<Button variant="primary" onclick={saveProfile} disabled={saving} loading={saving}>
							{saving ? m.settings_profile_saving() : m.settings_profile_save()}
						</Button>
					</div>
				</section>

				{#if user.auth_provider === 'local'}
					<section class="rounded-lg border border-border bg-bg-panel p-4">
						<h2 class="text-[13px] font-semibold">{m.settings_profile_change_password()}</h2>
						<div class="mt-3 flex flex-col gap-3">
							<PasswordInput
								label={m.settings_profile_current_password()}
								autocomplete="current-password"
								bind:value={currentPassword}
							/>
							<PasswordInput
								label={m.settings_profile_new_password()}
								autocomplete="new-password"
								bind:value={newPassword}
							/>
							<PasswordInput
								label={m.settings_profile_confirm_new_password()}
								autocomplete="new-password"
								bind:value={confirmPassword}
							/>
						</div>
						<div class="mt-4 flex justify-end">
							<Button
								variant="primary"
								onclick={changePassword}
								disabled={changingPassword}
								loading={changingPassword}
							>
								{changingPassword
									? m.settings_profile_changing()
									: m.settings_profile_change_password()}
							</Button>
						</div>
					</section>
				{/if}
			</div>
		{:else}
			<div class="mt-6 flex flex-col gap-3">
				<Skeleton height="11rem" />
				<Skeleton height="11rem" />
			</div>
		{/if}
	</div>
</div>
