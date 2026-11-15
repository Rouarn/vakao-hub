/**
 * 环境配置：唯一事实源。
 *
 * 只做「读取 + 类型转换 + 必要校验」，默认值集中在此，
 * 其它模块一律从这里取配置，不再直接读 process.env。
 */
import 'dotenv/config';
import { resolve } from 'node:path';

/**
 * 空值一律回落到默认值：`Number('')` 是 0，若不拦截，`PORT=`、`UPLOAD_MAX_BYTES=`
 * 这类留空写法会静默变成 0（前者监听随机端口，后者让所有上传被拒），与 toBool 的空值语义也不一致。
 */
function toInt(value: string | undefined, fallback: number): number {
  if (value === undefined || value.trim() === '') return fallback;
  const n = Number(value);
  return Number.isFinite(n) ? n : fallback;
}

function toBool(value: string | undefined, fallback: boolean): boolean {
  if (value === undefined || value === '') return fallback;
  return ['1', 'true', 'yes', 'on'].includes(value.toLowerCase());
}

function toList(value: string | undefined): string[] {
  return (value ?? '')
    .split(',')
    .map((item) => item.trim())
    .filter(Boolean);
}

const nodeEnv = process.env.NODE_ENV ?? 'development';
const isProduction = nodeEnv === 'production';

const jwtSecret =
  process.env.JWT_SECRET ?? (isProduction ? '' : 'vakao-hub-dev-secret-change-me');

if (!jwtSecret) {
  throw new Error('JWT_SECRET 必须配置');
}

if (isProduction && !process.env.ADMIN_PASSWORD) {
  throw new Error('生产环境必须显式配置 ADMIN_PASSWORD');
}

const corsOrigins = toList(process.env.CORS_ORIGINS);

export const env = {
  nodeEnv,
  isProduction,
  port: toInt(process.env.PORT, 3000),
  host: process.env.HOST ?? '127.0.0.1',
  publicBaseUrl: (process.env.PUBLIC_BASE_URL ?? '').replace(/\/+$/, ''),
  corsOrigins: corsOrigins.length
    ? corsOrigins
    : [
        'http://localhost:5180',
        'http://127.0.0.1:5180',
        'http://localhost:5200',
        'http://127.0.0.1:5200',
      ],

  jwt: {
    secret: jwtSecret,
    expiresIn: process.env.JWT_EXPIRES_IN ?? '12h',
  },

  admin: {
    username: process.env.ADMIN_USERNAME ?? 'admin',
    password: process.env.ADMIN_PASSWORD ?? 'admin123456',
  },

  db: {
    host: process.env.DB_HOST ?? '127.0.0.1',
    port: toInt(process.env.DB_PORT, 3306),
    username: process.env.DB_USER ?? 'root',
    password: process.env.DB_PASSWORD ?? '',
    database: process.env.DB_NAME ?? 'vakao_hub',
    synchronize: toBool(process.env.DB_SYNCHRONIZE, !isProduction),
    migrationsRun: toBool(process.env.DB_MIGRATIONS_RUN, false),
    logging: toBool(process.env.DB_LOGGING, false),
  },

  storageDir: resolve(process.cwd(), process.env.STORAGE_DIR ?? 'storage'),
  uploadMaxBytes: toInt(process.env.UPLOAD_MAX_BYTES, 2 * 1024 * 1024 * 1024),

  events: {
    /** 客户端事件保留天数；0（默认）表示永久保留，不启用清理任务。 */
    retentionDays: Math.max(0, toInt(process.env.EVENT_RETENTION_DAYS, 0)),
  },
} as const;

export type AppEnv = typeof env;

/**
 * 示例文件（.env.example / .env.production.example）里的占位值。
 * 这些串是公开的，照抄上线等于把签名密钥交给所有人，所以生产环境宁可拒绝启动。
 */
const PLACEHOLDER_VALUE = /^(?:replace-with-|please-change-this-)/i;

// 生产环境硬校验：避免把开发期行为带上线
if (isProduction) {
  // 占位值单独先拦：示例里的占位串长度通常已满足下面的长度要求，只查长度会漏过
  const secrets: ReadonlyArray<readonly [string, string]> = [
    ['JWT_SECRET', env.jwt.secret],
    ['ADMIN_PASSWORD', env.admin.password],
    ['DB_PASSWORD', env.db.password],
  ];
  for (const [name, value] of secrets) {
    if (PLACEHOLDER_VALUE.test(value)) {
      throw new Error(`生产环境 ${name} 仍是示例占位值，请替换为真实值`);
    }
  }

  if (!process.env.JWT_SECRET || env.jwt.secret.length < 32) {
    throw new Error('生产环境 JWT_SECRET 必须显式配置且至少 32 字符');
  }
  if (env.db.synchronize) {
    throw new Error('生产环境 DB_SYNCHRONIZE 必须为 false，结构变更请使用迁移');
  }
  if (env.admin.password.length < 8) {
    throw new Error('生产环境 ADMIN_PASSWORD 至少 8 位');
  }
}
