# SnowShot

App de Windows (janela própria, sem navegador) para **tirar prints da tela**: escolhe a área
com o mouse, captura a tela inteira ou a janela ativa, copia para a área de transferência e
salva um PNG. Fica na bandeja do sistema e responde a **atalhos globais**, mesmo minimizado.

Criado por Guilherme Souto. Faz parte da família Snow (junto com SnowToolkit e SnowDownloader).

## Baixar o programa

Os links abaixo sempre apontam para a versão mais recente:

- **Instalador (recomendado):** [SnowShot-Setup.exe](https://github.com/beemoon23/SnowShot/releases/latest/download/SnowShot-Setup.exe) — instala só para o seu usuário (não pede administrador) e cria atalho no menu Iniciar.
- **Portátil:** [SnowShot.exe](https://github.com/beemoon23/SnowShot/releases/latest/download/SnowShot.exe) — é só abrir, sem instalar.

Como o programa não tem assinatura digital, o Windows pode mostrar "O Windows protegeu o
computador" na primeira vez. Clique em **Mais informações → Executar assim mesmo**.

### Atualização automática

Ao abrir, o app confere se há versão nova no GitHub. Se houver, pergunta "Atualizar agora?",
baixa, troca o próprio `.exe` e reabre sozinho. Também dá para forçar pelo botão **Atualizar app**.
O título da janela mostra o código da versão (ex.: `SnowShot 7e80994`).

> Cópias compiladas fora do GitHub não têm código de versão e não se atualizam sozinhas.
> Baixe uma vez pelos links acima e dali em diante é automático.

## Como usar

| O que você quer                    | Atalho padrão          | Pelo botão / bandeja |
| ---------------------------------- | ---------------------- | -------------------- |
| Escolher uma área da tela          | `PrintScreen`          | **Área**             |
| Capturar o monitor onde está o mouse | `Ctrl + PrintScreen` | **Tela inteira**     |
| Capturar só a janela ativa         | `Shift + PrintScreen`  | **Janela ativa**     |

**Escolhendo a área:** a tela "congela" e escurece. Arraste o mouse sobre o que quer capturar e
solte. O tamanho aparece em tempo real. `Esc` ou o botão direito cancelam.

**Depois de capturar:** a imagem vai para a **área de transferência** (cole com `Ctrl+V` no
WhatsApp Web, Discord, Word, Paint...) e/ou para um **arquivo PNG** na pasta escolhida
(padrão: `Imagens\SnowShot`). Um aviso aparece perto do relógio.

### Opções

- **Atalhos:** cada ação tem uma lista de atalhos prontos (ou "Desativado"). Se algum estiver
  em uso por outro programa, o app avisa na barra de status.
- **Atraso:** 3, 5 ou 10 segundos antes de capturar. Serve para pegar menus e dicas que
  somem quando você clica.
- **Ao capturar:** copiar, salvar, ou os dois (pelo menos um fica sempre ligado).
- **Bandeja:** fechar ou minimizar manda o programa para a bandeja, e os atalhos continuam.
  Para sair de verdade, clique com o botão direito no ícone da bandeja → **Sair**.
- **Abrir junto com o Windows:** já abre na bandeja.
- **Últimas capturas:** duplo clique abre a imagem; dá para copiar de novo ou mostrar na pasta.

### Observação sobre o PrintScreen no Windows 11

O Windows 11 pode abrir a Ferramenta de Captura quando você aperta `PrintScreen`. Se isso
acontecer junto com o SnowShot, desligue em **Configurações → Acessibilidade → Teclado →
"Usar o botão Print Screen para abrir a captura de tela"**, ou escolha outro atalho no SnowShot
(por exemplo `Ctrl + Shift + S`).

## Como o GitHub compila e publica

A cada commit na `main`, o workflow **Build SnowShot** (aba **Actions**):

1. compila o `SnowShot.exe`, com o ícone e o código da versão embutidos;
2. gera o instalador `SnowShot-Setup.exe` (Inno Setup, script em `installer.iss`);
3. recria a release **latest** (aba **Releases**) com os dois arquivos. É dela que vêm os links
fixos acima e é nela que o app confere se há versão nova.

Para o ícone entrar no `.exe`, o arquivo `snow.ico` precisa estar na raiz do repositório
(o mesmo do SnowDownloader).

## Estrutura do projeto

| Arquivo               | O que faz                                                                    |
| --------------------- | ---------------------------------------------------------------------------- |
| `main.go`             | Janela, fluxo de captura, lista de capturas recentes                         |
| `capture.go`          | Foto da tela, recorte, PNG, área de transferência, janela/monitor alvo       |
| `overlay.go`          | Seletor de área (tela congelada, escurecida, com o retângulo do mouse)       |
| `hotkey.go`           | Atalhos globais (numa thread própria) e a lista de atalhos prontos           |
| `winapi.go`           | Chamadas diretas ao Windows (user32, gdi32, kernel32, dwmapi)                |
| `tray.go`             | Ícone da bandeja e menu                                                      |
| `update.go`           | Atualização automática do próprio app                                        |
| `settings.go`         | Opções salvas em `%APPDATA%\SnowShot\settings.json`                          |
| `autostart.go`        | "Abrir junto com o Windows"                                                  |
| `uiextras.go`         | Ícone do app e avisos da bandeja                                             |
| `shell_windows.go`    | Abrir arquivos e pastas no Explorer                                          |
| `installer.iss`       | Script do instalador                                                         |
| `.github/workflows/build.yml` | Compilação e publicação automáticas                                  |

## Compilar localmente (opcional)

```
go mod tidy
go install github.com/akavel/rsrc@latest
rsrc -manifest app.manifest -ico snow.ico -o rsrc.syso
go build -ldflags "-H=windowsgui -s -w" -o SnowShot.exe .
```

Compilada assim, a cópia fica sem código de versão e não se atualiza sozinha.

## Limites desta versão

- Só captura imagens estáticas (sem gravação de tela por enquanto).
- Ainda não tem editor (setas, texto, borrar). É o próximo passo.
- Janelas de programas com proteção de conteúdo (alguns players com DRM) saem pretas.
- Em computadores com monitores de escalas diferentes (100% e 150%, por exemplo) o app usa o
  tamanho real em pixels de cada tela.
