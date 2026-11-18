import { IsIn, IsString, Length, Matches } from 'class-validator';

export class UploadMediaDto {
  /** icon=应用图标（单图）；screens=详情页截图（多图） */
  @IsIn(['icon', 'screens'])
  scope!: 'icon' | 'screens';

  @IsString()
  @Length(1, 64)
  @Matches(/^[A-Za-z0-9][A-Za-z0-9_-]*$/, { message: 'appKey 非法' })
  appKey!: string;
}
