import 'reflect-metadata';
import { join } from 'node:path';
import { Logger, VersioningType } from '@nestjs/common';
import { NestFactory } from '@nestjs/core';
import type { NestExpressApplication } from '@nestjs/platform-express';
import { DocumentBuilder, SwaggerModule } from '@nestjs/swagger';
import compression from 'compression';
import cors from 'cors';
import helmet from 'helmet';
import { AppModule } from './app.module.js';
import { env } from './config/env.js';
import { ensureStorage } from './utils/paths.js';

async function bootstrap(): Promise<void> {
  await ensureStorage();

  const app = await NestFactory.create<NestExpressApplication>(AppModule);

  // 前面还有 Nginx 与 CDN，取 X-Forwarded-For 里的真实来源
  app.set('trust proxy', true);

  app.setGlobalPrefix('api');
  app.enableVersioning({
    type: VersioningType.URI,
    defaultVersion: '1',
    prefix: 'v',
  });

  app.use(
    helmet({
      contentSecurityPolicy: false,
      crossOriginResourcePolicy: { policy: 'cross-origin' },
    }),
  );
  app.use(compression());
  app.use(cors({ origin: env.corsOrigins, credentials: false }));

  // 前台与后台由同一个进程托管，Nginx 只需要整体反代到本服务
  const noCacheHtml = (res: import('express').Response, path: string): void => {
    if (path.endsWith('.html')) res.setHeader('Cache-Control', 'no-cache');
  };
  app.useStaticAssets(join(env.webDir, 'site'), {
    index: ['index.html'],
    maxAge: '1h',
    setHeaders: noCacheHtml,
  });
  // 上传的图标与截图：内容指纹命名，可长期缓存
  app.useStaticAssets(join(env.storageDir, 'media'), {
    prefix: '/api/v1/media/',
    maxAge: '365d',
    immutable: true,
  });
  app.useStaticAssets(join(env.webDir, 'admin'), {
    prefix: '/admin/',
    index: ['index.html'],
    maxAge: '1h',
    setHeaders: noCacheHtml,
  });

  // 生产环境默认关闭 Swagger，避免暴露内部接口结构
  if (!env.isProduction) {
    const swaggerConfig = new DocumentBuilder()
      .setTitle('VaKao Hub API')
      .setDescription('VaKao 软件中心：应用目录、版本发布、产物分发与更新检查')
      .setVersion('0.1.0')
      .addBearerAuth()
      .build();
    const document = SwaggerModule.createDocument(app, swaggerConfig);
    SwaggerModule.setup('api/docs', app, document);
  }

  app.enableShutdownHooks();

  await app.listen(env.port, env.host);
  Logger.log(
    `VaKao Hub 已启动：http://127.0.0.1:${env.port}/api/v1（文档 /api/docs）`,
    'Bootstrap',
  );
}

void bootstrap();
