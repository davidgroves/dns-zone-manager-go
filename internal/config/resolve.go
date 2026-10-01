package config

import "fmt"

func resolveSecrets(s *Settings) error {
	for i := range s.TSIGKeys {
		sec, err := resolveSecretField(s.TSIGKeys[i].Secret, s.TSIGKeys[i].SecretFile)
		if err != nil {
			return fmt.Errorf("tsig_keys[%s]: %w", s.TSIGKeys[i].Name, err)
		}
		s.TSIGKeys[i].Secret = sec
	}
	if s.Database.Postgres != nil {
		if err := resolvePostgresSecrets(s.Database.Postgres); err != nil {
			return err
		}
	}
	for i := range s.Webhooks.Targets {
		if err := resolveWebhookTarget(&s.Webhooks.Targets[i]); err != nil {
			return err
		}
	}
	return nil
}

func resolvePostgresSecrets(p *PostgresSettings) error {
	pass, err := resolveSecretField(p.Password, p.PasswordFile)
	if err != nil {
		return fmt.Errorf("database.postgres.password: %w", err)
	}
	p.Password = pass
	dsn, err := resolveSecretField(p.DSN, p.DSNFile)
	if err != nil {
		return fmt.Errorf("database.postgres.dsn: %w", err)
	}
	p.DSN = dsn
	return nil
}

func resolveWebhookTarget(t *WebhookTarget) error {
	url, err := resolveSecretField(t.URL, t.URLFile)
	if err != nil {
		return fmt.Errorf("webhooks.targets[%s].url: %w", t.Name, err)
	}
	t.URL = url
	a := &t.Auth
	sec, err := resolveSecretField(a.Secret, a.SecretFile)
	if err != nil {
		return fmt.Errorf("webhooks.targets[%s].auth.secret: %w", t.Name, err)
	}
	a.Secret = sec
	tok, err := resolveSecretField(a.Token, a.TokenFile)
	if err != nil {
		return fmt.Errorf("webhooks.targets[%s].auth.token: %w", t.Name, err)
	}
	a.Token = tok
	if a.Token.IsZero() && !a.Secret.IsZero() {
		a.Token = a.Secret
	}
	if a.Secret.IsZero() && !a.Token.IsZero() {
		a.Secret = a.Token
	}
	pass, err := resolveSecretField(a.Password, a.PasswordFile)
	if err != nil {
		return fmt.Errorf("webhooks.targets[%s].auth.password: %w", t.Name, err)
	}
	a.Password = pass
	return nil
}
