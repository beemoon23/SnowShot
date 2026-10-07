; Script do instalador (Inno Setup 6). O GitHub Actions compila este arquivo
; e gera o SnowShot-Setup.exe. Instala só para o usuário atual, então
; não pede senha de administrador e o programa consegue se atualizar sozinho.

#define MyAppName "SnowShot"
#ifndef AppVersion
  #define AppVersion "1.0"
#endif

[Setup]
AppId={{0638DF5A-DFD2-4490-A0C7-1414464A62C1}
AppName={#MyAppName}
AppVersion={#AppVersion}
AppPublisher=Gui
DefaultDirName={autopf}\{#MyAppName}
DefaultGroupName={#MyAppName}
DisableProgramGroupPage=yes
PrivilegesRequired=lowest
OutputDir=.
OutputBaseFilename=SnowShot-Setup
SetupIconFile=snow.ico
UninstallDisplayIcon={app}\SnowShot.exe
Compression=lzma2
SolidCompression=yes
WizardStyle=modern
CloseApplications=yes

[Languages]
Name: "brazilianportuguese"; MessagesFile: "compiler:Languages\BrazilianPortuguese.isl"

[Tasks]
Name: "desktopicon"; Description: "Criar um atalho na Área de Trabalho"; Flags: unchecked

[Files]
Source: "SnowShot.exe"; DestDir: "{app}"; Flags: ignoreversion

[Icons]
Name: "{autoprograms}\{#MyAppName}"; Filename: "{app}\SnowShot.exe"
Name: "{autodesktop}\{#MyAppName}"; Filename: "{app}\SnowShot.exe"; Tasks: desktopicon

[Run]
Filename: "{app}\SnowShot.exe"; Description: "Abrir o SnowShot"; Flags: nowait postinstall skipifsilent

[UninstallDelete]
Type: files; Name: "{app}\SnowShot.exe.old"
Type: files; Name: "{app}\SnowShot.exe.new"
