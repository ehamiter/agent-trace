FROM mcr.microsoft.com/devcontainers/base:ubuntu-24.04

# Create a non-root user (devcontainers do this by default with 'vscode' user)
USER vscode

# Install Claude Code
RUN curl -fsSL https://claude.ai/install.sh | bash

# Install any other tools you use locally (git is already included)
# RUN apt-get install -y python3 ripgrep fzf etc.
