package mailer

import (
	"fmt"
	"log/slog"

	"github.com/resend/resend-go/v2"
)

type Mailer struct {
	client *resend.Client
	from   string
	appURL string
	dev    bool // true when no API key — logs instead of sending
}

func New(apiKey, appURL string) *Mailer {
	if apiKey == "" {
		slog.Warn("RESEND_API_KEY not set — emails will print to stdout")
		return &Mailer{dev: true, appURL: appURL}
	}
	return &Mailer{
		client: resend.NewClient(apiKey),
		// TODO(prod): update the from address to match your verified Resend domain.
		// onboarding@resend.dev only delivers to the Resend account owner's inbox.
		from:   "App <onboarding@resend.dev>",
		appURL: appURL,
	}
}

func (m *Mailer) SendPasswordReset(toEmail, rawToken string) error {
	link := fmt.Sprintf("%s/reset-password?token=%s", m.appURL, rawToken)

	if m.dev {
		// Log only enough to confirm delivery in dev — never log the full token.
		slog.Info("password reset link (dev)", "email", toEmail, "link", link)
		return nil
	}

	_, err := m.client.Emails.Send(&resend.SendEmailRequest{
		From:    m.from,
		To:      []string{toEmail},
		Subject: "Reset your password",
		Html: fmt.Sprintf(`
<p>You requested a password reset.</p>
<p><a href="%s">Click here to reset your password</a></p>
<p>This link expires in 1 hour. If you didn't request this, you can ignore this email.</p>
`, link),
	})
	return err
}
