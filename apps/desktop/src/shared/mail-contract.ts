export interface MailServerSettings {
  readonly host: string;
  readonly port: number;
  readonly secure: boolean;
  readonly starttls: boolean;
  readonly rejectUnauthorized: boolean;
}

export interface SaveMailAccountInput {
  readonly id: string;
  readonly name: string;
  readonly email: string;
  readonly username: string;
  readonly password?: string;
  readonly enabled: boolean;
  readonly imap: MailServerSettings;
  readonly smtp: MailServerSettings;
}

export interface PublicMailAccount {
  readonly id: string;
  readonly name: string;
  readonly email: string;
  readonly username: string;
  readonly enabled: boolean;
  readonly hasCredential: boolean;
  readonly imap: MailServerSettings;
  readonly smtp: MailServerSettings;
}

export interface PublicMailSettings {
  readonly canPersistCredentials: boolean;
  readonly accounts: readonly PublicMailAccount[];
}
