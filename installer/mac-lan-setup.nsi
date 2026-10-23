; Per-user setup for mac-lan.exe. No administrator rights: the app's signed updater
; replaces the installed EXE in place, so it must live in a folder the user owns.
; Settings and caches under %LOCALAPPDATA%\MAC-DOW are kept on uninstall.
Unicode true
!include "MUI2.nsh"

!ifndef VERSION
  !error "Pass /DVERSION=0.1.0-alpha.N"
!endif
!ifndef NUMERIC_VERSION
  !error "Pass /DNUMERIC_VERSION=0.1.0.N"
!endif
!ifndef APP_EXE
  !error "Pass /DAPP_EXE=<path to mac-lan.exe>"
!endif
!ifndef OUT_FILE
  !error "Pass /DOUT_FILE=<path to mac-lan-setup.exe>"
!endif

!define UNINSTALL_KEY "Software\Microsoft\Windows\CurrentVersion\Uninstall\mac-lan"

Name "mac-lan"
OutFile "${OUT_FILE}"
InstallDir "$LOCALAPPDATA\Programs\mac-lan"
InstallDirRegKey HKCU "${UNINSTALL_KEY}" "InstallLocation"
RequestExecutionLevel user
SetCompressor /SOLID lzma
BrandingText "mac-lan ${VERSION}"
VIProductVersion "${NUMERIC_VERSION}"
VIAddVersionKey "ProductName" "mac-lan"
VIAddVersionKey "ProductVersion" "${VERSION}"
VIAddVersionKey "FileVersion" "${VERSION}"
VIAddVersionKey "FileDescription" "mac-lan setup"
VIAddVersionKey "LegalCopyright" "MIT License"

!define MUI_ABORTWARNING
!define MUI_WELCOMEPAGE_TEXT "This installs mac-lan ${VERSION} for your Windows account. No administrator access is needed.$\r$\n$\r$\nmac-lan keeps itself up to date after installation.$\r$\n$\r$\nClose mac-lan before continuing."
!define MUI_FINISHPAGE_RUN "$INSTDIR\mac-lan.exe"
!define MUI_FINISHPAGE_RUN_TEXT "Open mac-lan"
!insertmacro MUI_PAGE_WELCOME
!insertmacro MUI_PAGE_LICENSE "..\LICENSE"
!insertmacro MUI_PAGE_DIRECTORY
!insertmacro MUI_PAGE_COMPONENTS
!insertmacro MUI_PAGE_INSTFILES
!insertmacro MUI_PAGE_FINISH
!insertmacro MUI_UNPAGE_CONFIRM
!insertmacro MUI_UNPAGE_INSTFILES
!insertmacro MUI_LANGUAGE "English"

Section "mac-lan" SecApp
  SectionIn RO
  SetOutPath "$INSTDIR"
  File "/oname=mac-lan.exe" "${APP_EXE}"
  File "..\LICENSE"
  File "..\THIRD_PARTY_NOTICES.md"
  WriteUninstaller "$INSTDIR\Uninstall mac-lan.exe"
  CreateShortcut "$SMPROGRAMS\mac-lan.lnk" "$INSTDIR\mac-lan.exe"
  WriteRegStr HKCU "${UNINSTALL_KEY}" "DisplayName" "mac-lan"
  WriteRegStr HKCU "${UNINSTALL_KEY}" "DisplayVersion" "${VERSION}"
  WriteRegStr HKCU "${UNINSTALL_KEY}" "Publisher" "tbhtho"
  WriteRegStr HKCU "${UNINSTALL_KEY}" "URLInfoAbout" "https://github.com/tbhtho/mac-lan"
  WriteRegStr HKCU "${UNINSTALL_KEY}" "InstallLocation" "$INSTDIR"
  WriteRegStr HKCU "${UNINSTALL_KEY}" "DisplayIcon" "$INSTDIR\mac-lan.exe"
  WriteRegStr HKCU "${UNINSTALL_KEY}" "UninstallString" '"$INSTDIR\Uninstall mac-lan.exe"'
  WriteRegStr HKCU "${UNINSTALL_KEY}" "QuietUninstallString" '"$INSTDIR\Uninstall mac-lan.exe" /S'
  WriteRegDWORD HKCU "${UNINSTALL_KEY}" "NoModify" 1
  WriteRegDWORD HKCU "${UNINSTALL_KEY}" "NoRepair" 1
  WriteRegDWORD HKCU "${UNINSTALL_KEY}" "EstimatedSize" 147000
SectionEnd

Section "Desktop shortcut" SecDesktop
  CreateShortcut "$DESKTOP\mac-lan.lnk" "$INSTDIR\mac-lan.exe"
SectionEnd

!insertmacro MUI_FUNCTION_DESCRIPTION_BEGIN
  !insertmacro MUI_DESCRIPTION_TEXT ${SecApp} "The mac-lan app, a Start menu entry and an uninstaller."
  !insertmacro MUI_DESCRIPTION_TEXT ${SecDesktop} "Put a mac-lan shortcut on the desktop."
!insertmacro MUI_FUNCTION_DESCRIPTION_END

Section "Uninstall"
  Delete "$INSTDIR\mac-lan.exe"
  ; Update backups and leftovers the updater keeps beside the EXE.
  FindFirst $0 $1 "$INSTDIR\.MAC-DOW-*"
  backups:
    StrCmp $1 "" backups_done
    Delete "$INSTDIR\$1"
    RMDir /r "$INSTDIR\$1"
    FindNext $0 $1
    Goto backups
  backups_done:
  FindClose $0
  Delete "$INSTDIR\LICENSE"
  Delete "$INSTDIR\THIRD_PARTY_NOTICES.md"
  Delete "$INSTDIR\Uninstall mac-lan.exe"
  RMDir "$INSTDIR"
  Delete "$SMPROGRAMS\mac-lan.lnk"
  Delete "$DESKTOP\mac-lan.lnk"
  DeleteRegKey HKCU "${UNINSTALL_KEY}"
SectionEnd
