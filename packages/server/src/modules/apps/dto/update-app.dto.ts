import { Type } from 'class-transformer';
import {
  ArrayMaxSize,
  IsArray,
  IsOptional,
  IsString,
  IsUrl,
  Length,
  Matches,
  ValidateNested,
} from 'class-validator';
import { FeatureDto } from './create-app.dto.js';

export class UpdateAppDto {
  @IsOptional()
  @IsString()
  @Length(1, 128)
  name?: string;

  @IsOptional()
  @IsString()
  @Length(0, 512)
  description?: string;

  @IsOptional()
  @IsString()
  @Length(0, 512)
  iconUrl?: string;

  @IsOptional()
  @IsString()
  @Length(0, 32)
  category?: string;

  @IsOptional()
  @IsUrl({ require_protocol: true })
  @Length(0, 255)
  repoUrl?: string;

  @IsOptional()
  @IsUrl({ require_protocol: true })
  @Length(0, 255)
  homepage?: string;

  @IsOptional()
  @IsUrl({ require_protocol: true })
  @Length(0, 255)
  docsUrl?: string;

  @IsOptional()
  @IsUrl({ require_protocol: true })
  @Length(0, 255)
  releaseUrl?: string;

  @IsOptional()
  @Matches(/^#[0-9A-Fa-f]{6}$/, { message: 'themeColor 必须是 #RRGGBB' })
  themeColor?: string | null;

  @IsOptional()
  @IsString()
  @Length(0, 160)
  tagline?: string | null;

  @IsOptional()
  @IsArray()
  @ArrayMaxSize(8)
  @ValidateNested({ each: true })
  @Type(() => FeatureDto)
  features?: FeatureDto[] | null;

  @IsOptional()
  @IsArray()
  @ArrayMaxSize(12)
  @IsString({ each: true })
  @Length(1, 512, { each: true })
  screenshots?: string[] | null;
}
