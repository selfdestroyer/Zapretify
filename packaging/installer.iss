; Inno Setup Script for Zapretify
#define MyAppName "Zapretify"
#define MyAppVersion "1.2.2"
#define MyAppPublisher "selfdestroyer"
#define MyAppURL "https://github.com/selfdestroyer/Zapretify"
#define MyAppExeName "Zapretify.exe"

[Setup]
AppId={{8B2F36A7-33C2-4E51-8E1E-54A81794DE01}
AppName={#MyAppName}
AppVersion={#MyAppVersion}
AppPublisher={#MyAppPublisher}
AppPublisherURL={#MyAppURL}
AppSupportURL={#MyAppURL}
AppUpdatesURL={#MyAppURL}
DefaultDirName={autopf}\{#MyAppName}
DefaultGroupName={#MyAppName}
AllowNoIcons=yes
OutputDir=..\dist
OutputBaseFilename=Zapretify-Setup-v{#MyAppVersion}
SetupIconFile=..\assets\icon.ico
Compression=lzma2/ultra64
SolidCompression=yes
WizardStyle=modern
PrivilegesRequired=admin
ArchitecturesInstallIn64BitMode=x64

[Languages]
Name: "russian"; MessagesFile: "compiler:Languages\Russian.isl"
Name: "english"; MessagesFile: "compiler:Default.isl"

[Tasks]
Name: "desktopicon"; Description: "{cm:CreateDesktopIcon}"; GroupDescription: "{cm:AdditionalIcons}"
Name: "autostart"; Description: "Запускать Zapretify при входе в систему (в трее)"; GroupDescription: "Автозапуск:"
Name: "install_service"; Description: "Установить Zapret как системную службу Windows (100% бесшовный запуск без UAC)"; GroupDescription: "Системная служба:"

[Files]
Source: "..\Zapretify.exe"; DestDir: "{app}"; Flags: ignoreversion
Source: "..\bin\*"; DestDir: "{app}\bin"; Flags: ignoreversion recursesubdirs createallsubdirs
Source: "..\lists\*"; DestDir: "{app}\lists"; Flags: ignoreversion recursesubdirs createallsubdirs
Source: "..\assets\*"; DestDir: "{app}\assets"; Flags: ignoreversion recursesubdirs createallsubdirs
Source: "..\strategies.json"; DestDir: "{app}"; Flags: ignoreversion

[Icons]
Name: "{group}\{#MyAppName}"; Filename: "{app}\{#MyAppExeName}"
Name: "{group}\{cm:UninstallProgram,{#MyAppName}}"; Filename: "{uninstallexe}"
Name: "{autodesktop}\{#MyAppName}"; Filename: "{app}\{#MyAppExeName}"; Tasks: desktopicon

[Registry]
; Autostart in registry if task selected
Root: HKCU; Subkey: "Software\Microsoft\Windows\CurrentVersion\Run"; ValueType: string; ValueName: "{#MyAppName}"; ValueData: """{app}\{#MyAppExeName}"" --minimized"; Flags: uninsdeletevalue; Tasks: autostart

[Run]
; Optional service installation during setup
Filename: "{app}\bin\winws.exe"; Parameters: "--service --wf-tcp=80,443,2053,2083,2087,2096,8443 --wf-udp=443,19294-19344,50000-50100 --filter-udp=443 --hostlist=""{app}\lists\list-general.txt"" --dpi-desync=fake --dpi-desync-repeats=6 --dpi-desync-fake-quic=""{app}\bin\quic_initial_www_google_com.bin"""; Flags: runhidden; Tasks: install_service
; Launch application at the end of installation
Filename: "{app}\{#MyAppExeName}"; Description: "{cm:LaunchProgram,{#StringChange(MyAppName, '&', '&&')}}"; Flags: nowait postinstall skipifsilent

[UninstallRun]
; Clean up service and WinDivert drivers upon uninstallation
Filename: "net.exe"; Parameters: "stop zapret"; Flags: runhidden
Filename: "sc.exe"; Parameters: "delete zapret"; Flags: runhidden
Filename: "taskkill.exe"; Parameters: "/F /IM winws.exe"; Flags: runhidden
Filename: "net.exe"; Parameters: "stop WinDivert"; Flags: runhidden
Filename: "sc.exe"; Parameters: "delete WinDivert"; Flags: runhidden
