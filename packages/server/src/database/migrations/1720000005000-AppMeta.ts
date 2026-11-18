import type { MigrationInterface, QueryRunner } from 'typeorm';

/** 应用详情元数据：一句话定位、功能点、截图列表。 */
export class AppMeta1720000005000 implements MigrationInterface {
  name = 'AppMeta1720000005000';

  async up(queryRunner: QueryRunner): Promise<void> {
    if (!(await queryRunner.hasColumn('hub_apps', 'tagline'))) {
      await queryRunner.query(
        'ALTER TABLE `hub_apps` ADD COLUMN `tagline` varchar(160) NULL',
      );
    }
    if (!(await queryRunner.hasColumn('hub_apps', 'features'))) {
      await queryRunner.query(
        'ALTER TABLE `hub_apps` ADD COLUMN `features` json NULL',
      );
    }
    if (!(await queryRunner.hasColumn('hub_apps', 'screenshots'))) {
      await queryRunner.query(
        'ALTER TABLE `hub_apps` ADD COLUMN `screenshots` json NULL',
      );
    }
  }

  async down(queryRunner: QueryRunner): Promise<void> {
    for (const name of ['screenshots', 'features', 'tagline']) {
      if (!(await queryRunner.hasColumn('hub_apps', name))) continue;
      await queryRunner.query(`ALTER TABLE \`hub_apps\` DROP COLUMN \`${name}\``);
    }
  }
}
