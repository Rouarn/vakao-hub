import { Body, Controller, Get, NotFoundException, Param, ParseIntPipe, Post, Query, Req, Res } from '@nestjs/common';
import { ApiOperation, ApiParam, ApiTags } from '@nestjs/swagger';
import type { Request, Response } from 'express';
import { Public } from '../../common/public.decorator.js';
import { resolveStoragePath } from '../../utils/paths.js';
import { serveFile } from '../../utils/file-serve.js';
import { CheckUpdateQueryDto } from './dto/check-update-query.dto.js';
import { DownloadArtifactQueryDto } from './dto/download-artifact-query.dto.js';
import { ReportEventDto } from './dto/report-event.dto.js';
import { UpdatesService } from './updates.service.js';

/**
 * 下载计数去重：CDN 回源有时会对同一次下载发起两次请求（先中断、再整包重取），
 * 同一产物 + 同一来源 IP 在短窗口内只计一次，避免统计翻倍。
 */
const RECENT_DOWNLOAD_MS = 10_000;
const recentDownloads = new Map<string, number>();

function shouldCountDownload(artifactId: number, ip: string): boolean {
  const key = `${artifactId}:${ip}`;
  const now = Date.now();
  const last = recentDownloads.get(key);
  if (last !== undefined && now - last < RECENT_DOWNLOAD_MS) return false;
  recentDownloads.set(key, now);
  if (recentDownloads.size > 5000) {
    for (const [k, v] of recentDownloads) {
      if (now - v >= RECENT_DOWNLOAD_MS) recentDownloads.delete(k);
    }
  }
  return true;
}

@ApiTags('客户端 · 更新')
@Controller('updates')
export class UpdatesController {
  constructor(private readonly updatesService: UpdatesService) {}

  @Public()
  @Get('check')
  @ApiOperation({ summary: '检查更新（Android 与桌面共用）' })
  check(@Query() query: CheckUpdateQueryDto, @Req() req: Request) {
    return this.updatesService.check(query, req);
  }

  @Public()
  @Get('download/:id')
  @ApiParam({ name: 'id', description: '产物 ID' })
  @ApiOperation({ summary: '下载产物（支持 Range 断点续传）' })
  async download(
    @Param('id', ParseIntPipe) id: number,
    @Query() query: DownloadArtifactQueryDto,
    @Req() req: Request,
    @Res() res: Response,
  ): Promise<void> {
    const { artifact } = await this.updatesService.getDownloadable(id, query.deviceId);
    const fullPath = resolveStoragePath(artifact.relPath);

    let served = false;
    try {
      served = await serveFile(fullPath, req, res, {
        fileName: artifact.fileName,
        etag: `hub-${artifact.id}-${artifact.sha256.slice(0, 16)}`,
      });
    } catch {
      if (!res.headersSent) {
        throw new NotFoundException('安装包文件缺失');
      }
    }

    // 只在真正开始传输完整文件时计数：304/416 不算下载，断点续传的分段请求也不重复计数
    if (served && req.method === 'GET' && !req.headers.range && shouldCountDownload(id, req.ip ?? 'unknown')) {
      await this.updatesService.countDownload(id).catch(() => undefined);
    }
  }

  @Public()
  @Post('report')
  @ApiOperation({ summary: '上报检查/下载/安装/启动事件' })
  report(@Body() dto: ReportEventDto) {
    return this.updatesService.report(dto);
  }
}
