# acd-ctx como serviço systemd (usuário)

O proxy `acd-ctx serve` (127.0.0.1:8787) roda como serviço de usuário, sem root.
Requer systemd ativo (no WSL: `systemd=true` em `/etc/wsl.conf`).

## Unidade

Arquivo `~/.config/systemd/user/acd-ctx.service`:

```ini
[Unit]
Description=acd-ctx proxy (compressão de contexto do Claude)
After=network.target

[Service]
Type=simple
ExecStart=%h/go/bin/acd-ctx serve --listen 127.0.0.1:8787
Restart=on-failure
RestartSec=2

[Install]
WantedBy=default.target
```

## Instalação

```bash
systemctl --user daemon-reload
systemctl --user enable --now acd-ctx.service
```

Para subir sem login aberto: `loginctl enable-linger $USER`.

## Operação

```bash
systemctl --user start|stop|restart acd-ctx.service
systemctl --user status acd-ctx.service
journalctl --user -u acd-ctx.service -f      # logs
acd-ctx doctor claude | grep proxy           # saúde
```

## Observações

- Não rode `acd-ctx serve` à mão em paralelo: a porta 8787 fica ocupada e o serviço entra em loop de restart.
- Parar o serviço derruba as chamadas de qualquer sessão do Claude que use `ANTHROPIC_BASE_URL=http://127.0.0.1:8787`; reinicie fora de sessões em andamento.
- A configuração do projeto fica em `.acd-ctx/config.yaml` (fora do git), ex.: `window.context_limit: 200000`; reinicie o serviço após mudar.
