; Enter Space Light — Windows installer (Inno Setup 6+).
;
; Per-user install: no administrator rights for the app itself. WinFsp is a
; system driver, so if it is missing the bundled, unmodified WinFsp MSI runs
; and Windows asks for permission for that step only.
;
; Built by .github/workflows/release.yml:
;   iscc /DAppVersion=1.2.3 /DArch=amd64 /DSrc=<dir with exes> /DWinFspMsi=<path> esl.iss

#ifndef AppVersion
  #define AppVersion "0.0.0"
#endif
#ifndef Arch
  #define Arch "amd64"
#endif
#ifndef Src
  #define Src "..\..\dist\esl-" + AppVersion + "-windows-" + Arch
#endif
#ifndef WinFspMsi
  #define WinFspMsi "winfsp.msi"
#endif

[Setup]
AppId={{6B0E3C1E-5E8B-4F2B-9A64-0E5C7A1E5A11}
AppName=Enter Space Light
AppVersion={#AppVersion}
AppVerName=Enter Space Light {#AppVersion}
AppPublisher=Blaine Miller
AppPublisherURL=https://wemiller.com/apps/enter-space/
AppSupportURL=https://github.com/blaineam/enter-space-light/issues
DefaultDirName={localappdata}\Programs\Enter Space Light
DefaultGroupName=Enter Space Light
DisableProgramGroupPage=yes
PrivilegesRequired=lowest
PrivilegesRequiredOverridesAllowed=dialog
OutputBaseFilename=EnterSpaceLight-{#AppVersion}-windows-{#Arch}-setup
SetupIconFile=..\..\assets\app.ico
UninstallDisplayIcon={app}\EnterSpaceLight.exe
LicenseFile=..\..\LICENSE
InfoBeforeFile=..\..\NOTICE
Compression=lzma2/max
SolidCompression=yes
WizardStyle=modern
CloseApplications=yes
#if Arch == "arm64"
ArchitecturesAllowed=arm64
ArchitecturesInstallIn64BitMode=arm64
#else
ArchitecturesAllowed=x64compatible
ArchitecturesInstallIn64BitMode=x64compatible
#endif

[Files]
Source: "{#Src}\esl.exe"; DestDir: "{app}"; Flags: ignoreversion
Source: "{#Src}\EnterSpaceLight.exe"; DestDir: "{app}"; Flags: ignoreversion
Source: "..\..\LICENSE"; DestDir: "{app}"; Flags: ignoreversion
Source: "..\..\NOTICE"; DestDir: "{app}"; DestName: "NOTICE.txt"; Flags: ignoreversion
Source: "{#WinFspMsi}"; DestDir: "{tmp}"; DestName: "winfsp.msi"; Flags: deleteafterinstall; Check: NeedsWinFsp

[Icons]
; The AppUserModelID lets Windows show sync-failure notifications.
Name: "{group}\Enter Space Light"; Filename: "{app}\EnterSpaceLight.exe"; Parameters: "open"; AppUserModelID: "EnterSpaceLight"

[Run]
Filename: "msiexec.exe"; Parameters: "/i ""{tmp}\winfsp.msi"" /passive /norestart"; StatusMsg: "Installing the WinFsp driver (Windows will ask for permission)…"; Flags: shellexec waituntilterminated; Check: NeedsWinFsp
Filename: "{app}\EnterSpaceLight.exe"; Parameters: "run --open"; Description: "Start Enter Space Light"; Flags: nowait postinstall skipifsilent

[UninstallRun]
Filename: "{app}\esl.exe"; Parameters: "quit"; Flags: runhidden waituntilterminated; RunOnceId: "Quit"
Filename: "{app}\esl.exe"; Parameters: "autostart off"; Flags: runhidden waituntilterminated; RunOnceId: "Autostart"

[Code]
function WinFspInstalled(): Boolean;
var
  Dir: String;
begin
  Result := RegQueryStringValue(HKLM32, 'SOFTWARE\WinFsp', 'InstallDir', Dir) or
            RegQueryStringValue(HKLM64, 'SOFTWARE\WinFsp', 'InstallDir', Dir);
end;

function NeedsWinFsp(): Boolean;
begin
  Result := not WinFspInstalled();
end;

// Stop a running copy before files are replaced on upgrade.
function PrepareToInstall(var NeedsRestart: Boolean): String;
var
  Code: Integer;
begin
  if FileExists(ExpandConstant('{app}\esl.exe')) then
    Exec(ExpandConstant('{app}\esl.exe'), 'quit', '', SW_HIDE, ewWaitUntilTerminated, Code);
  Result := '';
end;
