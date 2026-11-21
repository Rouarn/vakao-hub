import {
  BadRequestException,
  Body,
  Controller,
  Delete,
  Post,
  Query,
  UploadedFile,
  UseInterceptors,
} from '@nestjs/common';
import { FileInterceptor } from '@nestjs/platform-express';
import { ApiConsumes, ApiOperation, ApiTags } from '@nestjs/swagger';
import { memoryStorage } from 'multer';
import { UploadMediaDto } from './dto/upload-media.dto.js';
import { MediaService } from './media.service.js';

const MAX_IMAGE_BYTES = 5 * 1024 * 1024;

@ApiTags('管理端 · 媒体')
@Controller('admin/media')
export class MediaController {
  constructor(private readonly media: MediaService) {}

  @Post()
  @UseInterceptors(
    FileInterceptor('file', {
      storage: memoryStorage(),
      limits: { fileSize: MAX_IMAGE_BYTES },
    }),
  )
  @ApiConsumes('multipart/form-data')
  @ApiOperation({ summary: '上传图标或详情页截图（≤5MB，PNG/JPEG/WebP）' })
  async upload(@Body() dto: UploadMediaDto, @UploadedFile() file?: Express.Multer.File) {
    if (!file) throw new BadRequestException('缺少文件');
    return this.media.save(dto.scope, dto.appKey, file);
  }

  @Delete()
  @ApiOperation({ summary: '删除站内上传的图片' })
  remove(@Query('path') path: string) {
    if (!path) throw new BadRequestException('缺少 path');
    return this.media.remove(path);
  }
}
