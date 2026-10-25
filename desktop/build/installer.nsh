; The engine may be running as the browser's native-messaging host; stop it so
; the new binary can be written (the app itself is closed by electron-builder).
!macro customInit
  nsExec::ExecToLog 'taskkill /F /IM vidhawk.exe'
!macroend

; Register / unregister the browser native-messaging host with the bundled engine.
!macro customInstall
  nsExec::ExecToLog '"$INSTDIR\resources\bin\vidhawk.exe" install'
!macroend

!macro customUnInstall
  nsExec::ExecToLog '"$INSTDIR\resources\bin\vidhawk.exe" uninstall'
!macroend
