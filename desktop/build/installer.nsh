; Register / unregister the browser native-messaging host with the bundled engine.
!macro customInstall
  nsExec::ExecToLog '"$INSTDIR\resources\bin\vidhawk.exe" install'
!macroend

!macro customUnInstall
  nsExec::ExecToLog '"$INSTDIR\resources\bin\vidhawk.exe" uninstall'
!macroend
