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

export class CreateAppDto {
  @IsString()
  @Length(1, 64)
  @Matches(/^[A-Za-z0-9][A-Za-z0-9_-]*$/, {
    message: 'appKey 仅允许字母、数字、下划线与短横线，且不能以符号开头',
  })
  appKey!: string;

  @IsString()
  @Length(1, 128)
  name!: string;

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

export class FeatureDto {
  @IsString()
  @Length(1, 64)
  title!: string;

  @IsString()
  @Length(0, 200)
  description!: string;
}
