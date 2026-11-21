#!/usr/bin/env python3
"""校验 macOS 发布归档的结构与版本，可选中与当前 .app 逐字节比对。

用法：
  python3 scripts/check-archive.py --archive a.zip --version 1.1.0 [--product VaKaoHub] [--app bin/VaKaoHub.app]

失败时打印明确原因并以非零退出，供 ship.sh 作为发布门禁。
"""

import argparse
import hashlib
import os
import plistlib
import sys
import zipfile

APPLE_DOUBLE = (".DS_Store",)


def fail(message: str) -> None:
    print(f"归档检查失败：{message}", file=sys.stderr)
    sys.exit(1)


def sha256(data: bytes) -> str:
    return hashlib.sha256(data).hexdigest()


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("--archive", required=True)
    parser.add_argument("--version", required=True)
    parser.add_argument("--product", default="VaKaoHub")
    parser.add_argument("--app", help="当前构建出的 .app，用于校验归档是否陈旧")
    args = parser.parse_args()

    app_name = f"{args.product}.app"
    plist_name = f"{app_name}/Contents/Info.plist"
    binary_name = f"{app_name}/Contents/MacOS/{args.product}"

    try:
        archive = zipfile.ZipFile(args.archive)
    except (OSError, zipfile.BadZipFile) as error:
        fail(f"无法打开归档 {args.archive}: {error}")

    with archive:
        infos = archive.infolist()
        names = [item.filename for item in infos]

        junk = [
            name
            for name in names
            if name.rsplit("/", 1)[-1] in APPLE_DOUBLE or name.rsplit("/", 1)[-1].startswith("._")
        ]
        if junk:
            fail("归档包含 AppleDouble/DS_Store: " + ", ".join(junk[:5]))

        top_level = {name.split("/", 1)[0] for name in names}
        if top_level != {app_name}:
            fail(f"归档顶层必须是单个 {app_name}，实际: {sorted(top_level)}")

        if f"{app_name}/" not in names:
            fail(f"归档缺少目录项 {app_name}/")

        if plist_name not in names:
            fail(f"归档缺少 {plist_name}")
        try:
            plist = plistlib.loads(archive.read(plist_name))
        except Exception as error:  # noqa: BLE001 - 解析失败一律视为门禁失败
            fail(f"无法解析 {plist_name}: {error}")
        actual_version = plist.get("CFBundleShortVersionString")
        if actual_version != args.version:
            fail(f"Info.plist 版本 {actual_version} 与期望 {args.version} 不一致")

        if binary_name not in names:
            fail(f"归档缺少可执行文件 {binary_name}")
        binary_info = archive.getinfo(binary_name)
        if binary_info.file_size == 0:
            fail(f"可执行文件 {binary_name} 为空")
        if not (binary_info.external_attr >> 16) & 0o111:
            fail(f"可执行文件 {binary_name} 没有执行权限")
        archive_binary = archive.read(binary_name)

    if args.app:
        app_binary = os.path.join(args.app, "Contents", "MacOS", args.product)
        app_plist = os.path.join(args.app, "Contents", "Info.plist")
        if not os.path.isfile(app_binary):
            fail(f"当前 .app 缺少可执行文件 {app_binary}")
        if not os.path.isfile(app_plist):
            fail(f"当前 .app 缺少 {app_plist}")
        with open(app_binary, "rb") as handle:
            if sha256(handle.read()) != sha256(archive_binary):
                fail(
                    "归档内可执行文件与当前 .app 不一致，可能复用了 --no-build 的陈旧包；"
                    "请去掉 --no-build 重新构建，或删除该归档后重试"
                )
        with open(app_plist, "rb") as handle:
            app_meta = plistlib.loads(handle.read())
        if app_meta.get("CFBundleShortVersionString") != args.version:
            fail(
                f"当前 .app 的 Info.plist 版本 {app_meta.get('CFBundleShortVersionString')} "
                f"与期望 {args.version} 不一致"
            )

    print(f"归档检查通过：{os.path.basename(args.archive)}（{args.version}，sha256 {sha256(archive_binary)[:12]}…）")


if __name__ == "__main__":
    main()
