@echo off
rem Minimal fake oci for Windows: modes ok and refresh-fail only.
if not "%FAKE_OCI_LOG%"=="" >>"%FAKE_OCI_LOG%" echo %*
if "%1 %2"=="session refresh" (
  if "%FAKE_OCI_MODE%"=="refresh-fail" (
    echo Error: session token is not valid or has expired 1>&2
    exit /b 1
  )
  if not "%FAKE_TOKEN_PATH%"=="" >"%FAKE_TOKEN_PATH%" echo|set /p="eyJhbGciOiJub25lIn0.eyJleHAiOjQxMDI0NDQ4MDB9.sig"
  exit /b 0
)
if "%1 %2"=="session validate" exit /b 0
if "%1 %2"=="session authenticate" (
  if not "%FAKE_TOKEN_PATH%"=="" >"%FAKE_TOKEN_PATH%" echo|set /p="eyJhbGciOiJub25lIn0.eyJleHAiOjQxMDI0NDQ4MDB9.sig"
  exit /b 0
)
echo fake oci: unsupported args: %* 1>&2
exit /b 2
