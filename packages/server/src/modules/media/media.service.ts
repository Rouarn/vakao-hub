import { BadRequestException, Injectable } from '@nestjs/common';
import { createHash } from 'node:crypto';
import { mkdir, rm, writeFile } from 'node:fs/promises';
import { join } from 'node:path';
import { env } from '../../config/env.js';

const EXT_BY_MIME: Record<string, string> = {
  'image/png': 'png',
  'image/jpeg': 'jpg',
  'image/webp': 'webp',
};

@Injectable()
export class MediaService {
  /** 保存上传的图片，返回站内绝对路径（以 /api/v1/media/ 开头） */
  async save(
    scope: 'icon' | 'screens',
    appKey: string,
    file: Express.Multer.File,
  ): Promise<{ path: string; size: number }> {
    const ext = EXT_BY_MIME[file.mimetype];
    if (!ext) {
      throw new BadRequestException('只支持 PNG / JPEG / WebP 图片');
    }

    const digest = createHash('sha256').update(file.buffer).digest('hex').slice(0, 12);
    const fileName = `${digest}.${ext}`;
    const relDir = scope === 'icon' ? join('icons') : join('screens', appKey);
    const absDir = join(env.storageDir, 'media', relDir);
    await mkdir(absDir, { recursive: true });

    // 图标：每个应用只留一张，替换时先清掉旧的
    if (scope === 'icon') {
      await rm(join(absDir, `${appKey}.png`), { force: true });
      await rm(join(absDir, `${appKey}.jpg`), { force: true });
      await rm(join(absDir, `${appKey}.webp`), { force: true });
    }

    const name = scope === 'icon' ? `${appKey}.${ext}` : fileName;
    await writeFile(join(absDir, name), file.buffer);

    const relPath = ['media', relDir.split('\\').join('/'), name].join('/');
    return { path: `/api/v1/${relPath}`, size: file.size };
  }

  /** 删除站内媒体（只允许删除 storage/media 下的文件） */
  async remove(mediaPath: string): Promise<{ success: true }> {
    const prefix = '/api/v1/media/';
    if (!mediaPath.startsWith(prefix)) {
      throw new BadRequestException('只能删除站内上传的图片');
    }
    const rel = mediaPath.slice(prefix.length);
    if (rel.includes('..')) throw new BadRequestException('非法路径');
    await rm(join(env.storageDir, 'media', rel), { force: true });
    return { success: true };
  }
}
