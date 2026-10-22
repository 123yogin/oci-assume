FROM --platform=linux/amd64 debian:bookworm-slim@sha256:7ff6ba31527b9e336162fa061746c366c4b2032f4d2cff2078bea43f78efe3f8
ARG PWSH_VERSION=7.4.6
RUN apt-get update \
 && apt-get install -y --no-install-recommends zsh bash fish cron ca-certificates curl libicu72 \
 && mkdir -p /opt/pwsh \
 && curl -fsSL -o /tmp/pwsh.tgz "https://github.com/PowerShell/PowerShell/releases/download/v${PWSH_VERSION}/powershell-${PWSH_VERSION}-linux-x64.tar.gz" \
 && echo "6f6015203c47806c5cc444c19d8ed019695e610fbd948154264bf9ca8e157561  /tmp/pwsh.tgz" | sha256sum -c - \
 && tar -xzf /tmp/pwsh.tgz -C /opt/pwsh && unlink /tmp/pwsh.tgz \
 && chmod +x /opt/pwsh/pwsh && ln -s /opt/pwsh/pwsh /usr/local/bin/pwsh \
 && apt-get purge -y curl && apt-get autoremove -y && apt-get clean
ENV DOTNET_SYSTEM_GLOBALIZATION_INVARIANT=1 POWERSHELL_TELEMETRY_OPTOUT=1
