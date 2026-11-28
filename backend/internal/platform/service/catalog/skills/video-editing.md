---
name: video-editing
description: 用 FFmpeg 剪辑、拼接、转码已有视频，或把上传的图片、视频和音频按顺序合成为可下载的 MP4。
---

在 task 的项目工作目录中使用受管 Bash。输入带 `attachment_id` 时，用 `attachment_get` 查询，按 `ready` 返回的项目相对路径取素材。上传中可稍后复查同一 ID，失败时指出具体文件。输入直接引用项目文件时沿用给定路径。保留原件，先用 `ffprobe -v error -show_streams -show_format -of json "$clip"` 核对时长、画面方向、尺寸和音轨，根据要求确定每段起止时间、顺序、画幅和声音来源。

精确剪切时重新编码。拼接前将各段统一为目标画布、像素宽高比和帧率，默认等比缩放并补边。保留原声时检查每段有无音轨，缺音段需要等长静音才能拼接音频。用户指定配乐时区分替换原声和混音，按画面长度裁切或补齐音频。

例如，取视频第 1 至 3 秒，片尾加 1 秒封面，用配乐替换原声。将 `clip`、`cover`、`music` 设为实际素材路径，`output` 设为本次新建目录中的 MP4 路径。按请求调整时间、音轨和画布。

```bash
canvas='scale=640:360:force_original_aspect_ratio=decrease:force_divisible_by=2,pad=640:360:(ow-iw)/2:(oh-ih)/2,setsar=1,fps=25,format=yuv420p'
ffmpeg -hide_banner -loglevel error -nostdin -n \
  -ss 1 -t 2 -i "$clip" -loop 1 -framerate 25 -t 1 -i "$cover" -i "$music" \
  -filter_complex "[0:v]${canvas}[v0];[1:v]${canvas}[v1];[v0][v1]concat=n=2:v=1:a=0[v];[2:a]apad=whole_dur=3[a]" \
  -map "[v]" -map "[a]" -t 3 -c:v libx264 -c:a aac -movflags +faststart "$output"
```

等待命令退出，使用 ffprobe 核对成片的时长、尺寸及预期音视频流，再用 `ffmpeg -v error -xerror -i "$output" -f null -` 完整解码。将通过验证的 MP4 保存在项目的 `outputs/` 下，通过 `artifact_publish` 传入项目相对路径，发布成功后回报文件名和时长。Web 素材上传及单份任务产物发布上限均为 32 MiB；若成片超限，可按用户的质量要求调整码率，或保留原画质并告知用户从「项目文件」下载。
