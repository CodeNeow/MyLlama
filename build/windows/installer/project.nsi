Unicode true

####
## Please note: Template replacements don't work in this file. They are provided with default defines like
## mentioned underneath.
## If the keyword is not defined, "wails_tools.nsh" will populate them with the values from ProjectInfo.
## If they are defined here, "wails_tools.nsh" will not touch them. This allows to use this project.nsi manually
## from outside of Wails for debugging and development of the installer.
##
## For development first make a wails nsis build to populate the "wails_tools.nsh":
## > wails build --target windows/amd64 --nsis
## Then you can call makensis on this file with specifying the path to your binary:
## For a AMD64 only installer:
## > makensis -DARG_WAILS_AMD64_BINARY=..\..\bin\app.exe
## For a ARM64 only installer:
## > makensis -DARG_WAILS_ARM64_BINARY=..\..\bin\app.exe
## For a installer with both architectures:
## > makensis -DARG_WAILS_AMD64_BINARY=..\..\bin\app-amd64.exe -DARG_WAILS_ARM64_BINARY=..\..\bin\app-arm64.exe
####
## The following information is taken from the ProjectInfo file, but they can be overwritten here.
####
## !define INFO_PROJECTNAME    "MyProject" # Default "{{.Name}}"
## !define INFO_COMPANYNAME    "MyCompany" # Default "{{.Info.CompanyName}}"
## !define INFO_PRODUCTNAME    "MyProduct" # Default "{{.Info.ProductName}}"
## !define INFO_PRODUCTVERSION "1.0.0"     # Default "{{.Info.ProductVersion}}"
## !define INFO_COPYRIGHT      "Copyright" # Default "{{.Info.Copyright}}"
###
## !define PRODUCT_EXECUTABLE  "Application.exe"      # Default "${INFO_PROJECTNAME}.exe"
## !define UNINST_KEY_NAME     "UninstKeyInRegistry"  # Default "${INFO_COMPANYNAME}${INFO_PRODUCTNAME}"
####
## !define REQUEST_EXECUTION_LEVEL "admin"            # Default "admin"  see also https://nsis.sourceforge.io/Docs/Chapter4.html
####
## Legacy uninstall keys: the uninstall key name changed with the installer
## eras (CompanyName/ProductName fell back to the project name before the
## wails.json attribution change, then stayed on the pre-rebrand
## "llama-desktop"/"Llama Desktop" pair for one release to keep the key
## stable, and became "CodeNeowMyLlama" with the full rebrand). .onInit only
## reads back the current-era key (legacy InstallLocations are not prefilled
## — the directory page must default to %PROGRAMFILES64%\MyLlama); the
## install section migrates a legacy install's data into $INSTDIR and drops
## the superseded keys after writing the current one.
!define UNINST_KEY_LEGACY_V039 "Software\Microsoft\Windows\CurrentVersion\Uninstall\llama-desktopLlama Desktop"
!define UNINST_KEY_LEGACY_V03X "Software\Microsoft\Windows\CurrentVersion\Uninstall\llama-desktopllama-desktop"
!define UNINST_KEY_LEGACY_V01X "Software\Microsoft\Windows\CurrentVersion\Uninstall\llama-guillama-gui"
####
## Include the wails tools
####
!include "wails_tools.nsh"

# The version information for this two must consist of 4 parts
VIProductVersion "${INFO_PRODUCTVERSION}.0"
VIFileVersion    "${INFO_PRODUCTVERSION}.0"

VIAddVersionKey "CompanyName"     "${INFO_COMPANYNAME}"
VIAddVersionKey "FileDescription" "${INFO_PRODUCTNAME} Installer"
VIAddVersionKey "ProductVersion"  "${INFO_PRODUCTVERSION}"
VIAddVersionKey "FileVersion"     "${INFO_PRODUCTVERSION}"
VIAddVersionKey "LegalCopyright"  "${INFO_COPYRIGHT}"
VIAddVersionKey "ProductName"     "${INFO_PRODUCTNAME}"

# Enable HiDPI support. https://nsis.sourceforge.io/Reference/ManifestDPIAware
ManifestDPIAware true

!include "MUI.nsh"

!define MUI_ICON "..\icon.ico"
!define MUI_UNICON "..\icon.ico"
# !define MUI_WELCOMEFINISHPAGE_BITMAP "resources\leftimage.bmp" #Include this to add a bitmap on the left side of the Welcome Page. Must be a size of 164x314
!define MUI_FINISHPAGE_NOAUTOCLOSE # Wait on the INSTFILES page so the user can take a look into the details of the installation steps
!define MUI_ABORTWARNING # This will warn the user if they exit from the installer.

!insertmacro MUI_PAGE_WELCOME # Welcome to the installer page.
# !insertmacro MUI_PAGE_LICENSE "resources\eula.txt" # Adds a EULA page to the installer
!insertmacro MUI_PAGE_DIRECTORY # In which folder install page.
!insertmacro MUI_PAGE_INSTFILES # Installing page.
!insertmacro MUI_PAGE_FINISH # Finished installation page.

!insertmacro MUI_UNPAGE_INSTFILES # Uinstalling page

!insertmacro MUI_LANGUAGE "English" # Set the Language of the installer

## The following two statements can be used to sign the installer and the uninstaller. The path to the binaries are provided in %1
#!uninstfinalize 'signtool --file "%1"'
#!finalize 'signtool --file "%1"'

Name "${INFO_PRODUCTNAME}"
OutFile "..\..\bin\${INFO_PROJECTNAME}-${ARCH}-installer.exe" # Name of the installer's file.
InstallDir "$PROGRAMFILES64\${INFO_PRODUCTNAME}" # Default installing folder ($PROGRAMFILES is Program Files folder).
ShowInstDetails show # This will always show the installation details.

Function .onInit
   !insertmacro wails.checkArchitecture

   ; 覆盖安装时读回上次自定义安装路径（InstallLocation）
   ; 背景：wails.writeUninstaller 用 SetRegView 64 把卸载信息写入 64 位注册表视图，
   ; 而 NSIS 的 InstallDirRegKey 指令在 .onInit 之前执行且不受运行时 SetRegView 影响，
   ; 32 位安装器默认只读 32 位视图，直接使用 InstallDirRegKey 会因视图错配而读不到上次路径。
   ; 因此这里在 .onInit 中手动 SetRegView 64 后 ReadRegStr，读到非空值即覆盖 $INSTDIR，
   ; 使 MUI_PAGE_DIRECTORY 默认显示上次安装目录，实现覆盖安装记住自定义路径。
   SetRegView 64
   ReadRegStr $0 HKLM "${UNINST_KEY}" "InstallLocation"
   ; 仅回读新时代键（CodeNeowMyLlama），不再回读 llama-desktop / llama-gui
   ; 时代的旧键：旧键的 InstallLocation 指向旧品牌默认目录（如
   ; %PROGRAMFILES64%\llama-desktop\Llama Desktop），若据此预填，会把默认路径
   ; 用户拉回旧目录，使向 %PROGRAMFILES64%\MyLlama 的品牌迁移永不生效。旧版数据
   ; 由安装段的 wails.migrateLegacyInstall 迁移，与所选目录无关；希望原位升级的
   ; 自定义路径用户仍可在目录页手动输入旧目录（宏内的同目录守卫会正确处理该情况）。
   ;
   ; Only the current-era key is read back. Legacy-era InstallLocations are
   ; deliberately NOT prefilled anymore: the install section migrates legacy
   ; data regardless of the chosen directory, so the directory page defaults
   ; to %PROGRAMFILES64%\MyLlama; users wanting an in-place upgrade can still
   ; type their legacy dir manually (the macro's same-dir guard covers it).
   ${If} $0 != ""
       StrCpy $INSTDIR $0
   ${EndIf}
FunctionEnd

# Legacy install migration ------------------------------------------------
#
# Moves a superseded Llama Desktop install's data into $INSTDIR so the
# rebranded app picks up where the old one left off:
#   1. llama-desktop-config.json -> $INSTDIR (the app renames it to
#      myllama-config.json on first start);
#   2. LLM-Models\ and llama-cpp\ move in (same-volume Rename first,
#      CopyFiles fallback — the source is only removed after the copy
#      provably landed);
#   3. the docs cache is skipped (it re-fetches on demand);
#   4. when the legacy directory holds no data anymore, the
#      migration-legacy-path.txt marker is planted and the legacy uninstaller
#      runs silently from a temp copy, dropping its registry key; if anything
#      failed to move, the legacy install is left fully in place (Add/Remove
#      entry kept) — migration never blocks or fails the install.
#
# Marker contract: the marker is ONLY written on a complete migration.
#   marker present   = "data moved, the app rewrites the config's recorded
#                      absolute paths from the legacy prefix to $INSTDIR";
#   no marker        = "legacy kept in place, the recorded legacy paths stay
#                      valid and must be left untouched" (rewriting them would
#                      point the model list at $INSTDIR while the models still
#                      sit in the legacy dir).
!macro wails.migrateLegacyDir SRC DST
    ${If} ${FileExists} "${SRC}\*"
        Rename "${SRC}" "${DST}"
        ${If} ${FileExists} "${SRC}\*" ; rename failed (cross-volume or existing target)
            CreateDirectory "${DST}"
            CopyFiles /SILENT "${SRC}\*" "${DST}"
            ${If} ${FileExists} "${DST}\*"
                RMDir /r "${SRC}" ; only after a successful copy
            ${EndIf}
        ${EndIf}
    ${EndIf}
!macroend

!macro wails.migrateLegacyInstall LEGACY_KEY
    SetRegView 64
    ReadRegStr $R9 HKLM "${LEGACY_KEY}" "InstallLocation"
    ${If} $R9 != ""
    ${AndIf} $R9 != "$INSTDIR" ; StrCmp compares case-insensitively
    ${AndIf} $R9 != "$INSTDIR\"
        DetailPrint "Migrating data from legacy install: $R9"
        ; 1. legacy config (only when the new install has none yet)
        ${If} ${FileExists} "$R9\llama-desktop-config.json"
        ${AndIfNot} ${FileExists} "$INSTDIR\myllama-config.json"
            CopyFiles /SILENT "$R9\llama-desktop-config.json" "$INSTDIR"
        ${EndIf}
        ; 2. model library + llama.cpp runtime (docs cache is re-fetchable: skipped)
        !insertmacro wails.migrateLegacyDir "$R9\LLM-Models" "$INSTDIR\LLM-Models"
        !insertmacro wails.migrateLegacyDir "$R9\llama-cpp" "$INSTDIR\llama-cpp"
        ; 3+4. only a COMPLETE migration (legacy dir emptied) writes the
        ;      marker and uninstalls the legacy install: the marker tells the
        ;      app "your recorded paths were rewritten to $INSTDIR, your data
        ;      moved". In the partial branch below no marker is written, so
        ;      the app keeps the legacy absolute paths — still valid because
        ;      the legacy install is left fully in place.
        ${IfNot} ${FileExists} "$R9\*"
            FileOpen $R8 "$INSTDIR\migration-legacy-path.txt" w
            ${If} $R8 != ""
                FileWrite $R8 "$R9"
                FileClose $R8
            ${EndIf}
            ${If} ${FileExists} "$R9\uninstall.exe"
                CreateDirectory "$TEMP\MyLlamaMigrate"
                CopyFiles /SILENT "$R9\uninstall.exe" "$TEMP\MyLlamaMigrate"
                ExecWait '"$TEMP\MyLlamaMigrate\uninstall.exe" /S _?=$R9'
                Delete "$TEMP\MyLlamaMigrate\uninstall.exe"
                RMDir "$TEMP\MyLlamaMigrate"
            ${EndIf}
            DeleteRegKey HKLM "${LEGACY_KEY}"
        ${Else}
            DetailPrint "Legacy data left in place ($R9): the copy did not complete, legacy install kept, recorded paths left untouched"
        ${EndIf}
    ${Else}
        ; Superseded era key without a distinct install location (absent or
        ; overlay install into the same dir): drop the stale entry.
        DeleteRegKey HKLM "${LEGACY_KEY}"
    ${EndIf}
!macroend

Section
    !insertmacro wails.setShellContext

    !insertmacro wails.webview2runtime

    SetOutPath $INSTDIR

    !insertmacro wails.files

    # legacy-named binary from pre-MyLlama installers
    Delete "$INSTDIR\llama-desktop.exe"

    # Legacy Llama Desktop install migration, newest era first
    !insertmacro wails.migrateLegacyInstall "${UNINST_KEY_LEGACY_V039}"
    !insertmacro wails.migrateLegacyInstall "${UNINST_KEY_LEGACY_V03X}"
    !insertmacro wails.migrateLegacyInstall "${UNINST_KEY_LEGACY_V01X}"

    CreateShortcut "$SMPROGRAMS\${INFO_PRODUCTNAME}.lnk" "$INSTDIR\${PRODUCT_EXECUTABLE}"
    CreateShortCut "$DESKTOP\${INFO_PRODUCTNAME}.lnk" "$INSTDIR\${PRODUCT_EXECUTABLE}"

    !insertmacro wails.associateFiles
    !insertmacro wails.associateCustomProtocols

    !insertmacro wails.writeUninstaller

    ; 把本次安装路径写入注册表（64 位视图，与 wails.writeUninstaller 的写入视图保持一致；
    ; 宏内部已 SetRegView 64，这里再显式设置一次是幂等的，仅作防御），
    ; 供下次覆盖安装时在 .onInit 中读回 InstallLocation。
    SetRegView 64
    WriteRegStr HKLM "${UNINST_KEY}" "InstallLocation" "$INSTDIR"
    ; 旧时代的卸载键由上方的 wails.migrateLegacyInstall 统一清理
    ; （数据迁移成功后删除；迁移不完整时保留条目以便用户手动卸载）。
SectionEnd

Section "uninstall"
    !insertmacro wails.setShellContext

    RMDir /r "$AppData\${PRODUCT_EXECUTABLE}" # Remove the WebView2 DataPath

    RMDir /r $INSTDIR

    Delete "$SMPROGRAMS\${INFO_PRODUCTNAME}.lnk"
    Delete "$DESKTOP\${INFO_PRODUCTNAME}.lnk"

    !insertmacro wails.unassociateFiles
    !insertmacro wails.unassociateCustomProtocols

    !insertmacro wails.deleteUninstaller
SectionEnd
