; Inno Setup installer Karta untuk Windows.
; Dipakai workflow release: ISCC /DAppVersion=<versi> packaging/karta.iss
; Binary portable harus sudah ada di dist/karta-windows-amd64-portable.exe.

#define AppName "Karta"
#define AppPublisher "dchya24"
#define AppURL "https://github.com/dchya24/karta"

[Setup]
AppId={{8A3F6C42-9D51-4E07-B2A8-63D0C4E1F5B9}
AppName={#AppName}
AppVersion={#AppVersion}
AppPublisher={#AppPublisher}
AppPublisherURL={#AppURL}
AppSupportURL={#AppURL}
DefaultDirName={autopf}\{#AppName}
DefaultGroupName={#AppName}
UninstallDisplayIcon={app}\karta.ico
OutputDir=..\dist
OutputBaseFilename=Karta-Setup-windows-amd64
SetupIconFile=..\assets\icons\karta.ico
Compression=lzma2
SolidCompression=yes
WizardStyle=modern
ArchitecturesInstallIn64BitMode=x64compatible
ArchitecturesAllowed=x64compatible
PrivilegesRequiredOverridesAllowed=dialog

[Tasks]
Name: "desktopicon"; Description: "{cm:CreateDesktopIcon}"; GroupDescription: "{cm:AdditionalIcons}"

[Files]
Source: "..\dist\karta-windows-amd64-portable.exe"; DestName: "karta.exe"; DestDir: "{app}"; Flags: ignoreversion
Source: "..\assets\icons\karta.ico"; DestName: "karta.ico"; DestDir: "{app}"; Flags: ignoreversion

[Icons]
Name: "{group}\{#AppName}"; Filename: "{app}\karta.exe"
Name: "{group}\Uninstall {#AppName}"; Filename: "{uninstallexe}"
Name: "{autodesktop}\{#AppName}"; Filename: "{app}\karta.exe"; Tasks: desktopicon

[Run]
Filename: "{app}\karta.exe"; Description: "{cm:LaunchProgram,{#AppName}}"; Flags: nowait postinstall skipifsilent
