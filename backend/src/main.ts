import { Logger } from '@nestjs/common';
import { ConfigService } from '@nestjs/config';
import { NestFactory } from '@nestjs/core';
import { DocumentBuilder, SwaggerModule } from '@nestjs/swagger';
import helmet from 'helmet';
import { json, urlencoded } from 'express';
import { AppModule } from './app.module';

async function bootstrap(): Promise<void> {
  const app = await NestFactory.create(AppModule, {
    bufferLogs: false,
    bodyParser: false,
  });
  const logger = new Logger('Bootstrap');
  const config = app.get(ConfigService);

  // Security headers.
  app.use(helmet());

  // Request body limits (explicit; default parser disabled above).
  app.use(json({ limit: '1mb' }));
  app.use(urlencoded({ extended: true, limit: '1mb' }));

  // Trust proxy: configurable, safe default ('loopback'). Required for
  // correct client IPs in rate limiting when behind a reverse proxy.
  const trustProxy = process.env.TRUST_PROXY ?? 'loopback';
  const expressApp = app.getHttpAdapter().getInstance() as {
    set: (key: string, value: unknown) => void;
  };
  const trustValue = /^\d+$/.test(trustProxy)
    ? parseInt(trustProxy, 10)
    : trustProxy;
  expressApp.set('trust proxy', trustValue);

  // CORS from environment (never '*' for credentialed auth).
  const origins = config.get<string[]>('corsOrigins', []);
  app.enableCors({ origin: origins, credentials: true });

  // Single global version prefix. Controllers use @Controller('auth'),
  // @Controller('users') etc. Final routes: /api/v1/auth/..., /api/v1/users/...
  app.setGlobalPrefix('api/v1');

  // OpenAPI documentation.
  const swaggerConfig = new DocumentBuilder()
    .setTitle('Campus Hive API')
    .setDescription('Campus Hive backend - Identity + Authentication (Slice 1)')
    .setVersion('1.0')
    .addBearerAuth(
      { type: 'http', scheme: 'bearer', bearerFormat: 'JWT' },
      'access-token',
    )
    .addTag('auth')
    .addTag('users')
    .build();
  const document = SwaggerModule.createDocument(app, swaggerConfig);
  SwaggerModule.setup('api/docs', app, document);

  const port = config.get<number>('port', 3000);
  await app.listen(port);
  logger.log(`Campus Hive backend listening on port ${port}`);
}

void bootstrap();
