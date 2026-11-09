import { Injectable, Logger } from '@nestjs/common';
import { ConfigService } from '@nestjs/config';

export interface OutgoingEmail {
  to: string;
  subject: string;
  text: string;
}

/**
 * Email abstraction. Development default is a console adapter that logs
 * metadata only (recipient + subject, never tokens or credentials).
 * Production delivery is configured via EMAIL_* environment variables;
 * until a provider is wired, the service refuses to silently drop mail
 * and logs the attempt instead.
 */
@Injectable()
export class EmailService {
  private readonly logger = new Logger(EmailService.name);

  constructor(private readonly config: ConfigService) {}

  private get configured(): boolean {
    return Boolean(this.config.get<string>('email.host', ''));
  }

  async sendVerificationEmail(to: string, verifyUrl: string): Promise<void> {
    await this.send({
      to,
      subject: 'Verify your Campus Hive email',
      text: `Verify your email by visiting: ${verifyUrl}`,
    });
  }

  async sendPasswordResetEmail(to: string, resetUrl: string): Promise<void> {
    await this.send({
      to,
      subject: 'Reset your Campus Hive password',
      text: `Reset your password by visiting: ${resetUrl}`,
    });
  }

  private async send(mail: OutgoingEmail): Promise<void> {
    if (!this.configured) {
      // Dev adapter: metadata only. Never log bodies (they contain tokens).
      this.logger.log(
        `[dev-email] to=${mail.to} subject="${mail.subject}" (no EMAIL_HOST configured)`,
      );
      return;
    }
    // Provider wiring point (SMTP/SendGrid). Until implemented for a real
    // provider, fail loudly rather than pretending delivery succeeded.
    this.logger.warn(
      `[email] provider not wired; email to ${mail.to} ("${mail.subject}") was NOT delivered. Configure an email provider.`,
    );
  }
}
