#!/usr/bin/env bash
set -euo pipefail

# These packages all come from Ubuntu. Use the runner's distro mirrors without
# contacting unrelated vendor feeds preinstalled on the hosted image.
source_file=/etc/apt/sources.list.d/ubuntu.sources
if [[ ! -s $source_file ]]; then
  source_file=/etc/apt/sources.list
fi
[[ -s $source_file ]]
apt_sources=(-o "Dir::Etc::sourcelist=$source_file" -o "Dir::Etc::sourceparts=-")
sudo apt-get "${apt_sources[@]}" update
sudo apt-get "${apt_sources[@]}" install -y build-essential python3 procps ripgrep fd-find
sudo ln -sf /usr/bin/fdfind /usr/local/bin/fd
