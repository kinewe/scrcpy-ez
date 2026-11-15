#!/usr/bin/env bash
set -euo pipefail

# Run from an empty build directory under the MSYS2 MINGW64 shell.
# FFMPEG_SOURCE should point to FFmpeg 9.0.2 using a relative path.
# PKG_CONFIG_PATH should resolve zlib, dav1d and nv-codec-headers 13.0.19.0.
: "${FFMPEG_SOURCE:?Set FFMPEG_SOURCE to the FFmpeg source directory}"
: "${FFMPEG_DESTDIR:?Set FFMPEG_DESTDIR to the staging directory}"
export FFMPEG_SOURCE_WIN="$(cd "$FFMPEG_SOURCE" && pwd -W)"
export FFMPEG_SOURCE_POSIX="$(cd "$FFMPEG_SOURCE" && pwd)"
cat > ffmpeg-cc <<'EOF'
#!/usr/bin/env bash
exec gcc "-ffile-prefix-map=${FFMPEG_SOURCE_WIN}=ffmpeg" \
  "-ffile-prefix-map=${FFMPEG_SOURCE_POSIX}=ffmpeg" "$@"
EOF
chmod +x ffmpeg-cc
"${FFMPEG_SOURCE}/configure" \
  --cc=./ffmpeg-cc --prefix=/usr/local --pkg-config-flags=--static --disable-autodetect \
  --target-os=mingw32 --arch=x86_64 --extra-cflags='-O2 -fPIC' \
  --extra-ldflags=-static-libgcc --disable-debug --disable-programs --disable-doc \
  --enable-swscale --disable-avfilter --disable-network --disable-everything \
  --disable-vulkan --disable-vdpau --enable-swresample --enable-libdav1d \
  --enable-decoder=h264,hevc,av1,vp8,vp9,libdav1d,pcm_s16le,opus,aac,flac,png,bmp,mjpeg,webp,gif \
  --enable-encoder=mjpeg,h264_nvenc,h264_mf --enable-nvenc --enable-ffnvcodec \
  --enable-mediafoundation --enable-w32threads --enable-protocol=file \
  --enable-demuxer=image2 --enable-parser=png,h264 --enable-zlib \
  --enable-muxer=matroska,mp4,opus,flac,wav --disable-avdevice --disable-vaapi \
  --enable-d3d11va \
  --enable-hwaccel=h264_d3d11va,h264_d3d11va2,hevc_d3d11va,hevc_d3d11va2,av1_d3d11va,av1_d3d11va2,vp9_d3d11va,vp9_d3d11va2 \
  --disable-static --enable-shared
make -j"${FFMPEG_JOBS:-8}"
make DESTDIR="$FFMPEG_DESTDIR" install
