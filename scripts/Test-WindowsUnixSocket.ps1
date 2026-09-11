#requires -Version 7.0
<#
.SYNOPSIS
Probe Windows AF_UNIX without OpsKat, a database, credentials or remote hosts.
.DESCRIPTION
Each pair uses the same fresh ASCII socket basename. Existing application
endpoints are never opened, removed or renamed. Directories must already exist.
JSON Lines records distinguish socket/bind/listen/connect/accept/transfer/cleanup.
Run from the same normal user context as OpsKat, not a restricted execution sandbox.
.EXAMPLE
./scripts/Test-WindowsUnixSocket.ps1 -BindDirectory C:\ipc-a,D:\ipc-a,C:\ipc-j
.EXAMPLE
./scripts/Test-WindowsUnixSocket.ps1 -BindDirectory C:\ipc-j -ConnectDirectory D:\ipc-a
#>
[CmdletBinding()]
param(
    [Parameter(Mandatory)] [string[]] $BindDirectory,
    [string[]] $ConnectDirectory,
    [ValidateRange(100, 30000)] [int] $TimeoutMs = 3000
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'
if (-not $IsWindows) { throw 'This probe requires Windows and PowerShell 7.' }
if (-not $ConnectDirectory) { $ConnectDirectory = $BindDirectory }
if ($BindDirectory.Count -ne $ConnectDirectory.Count) { throw 'Directory arrays must have equal lengths.' }

$identity = [Security.Principal.WindowsIdentity]::GetCurrent()
$principal = [Security.Principal.WindowsPrincipal]::new($identity)
$run = [guid]::NewGuid().ToString('N')
$basename = 'probe-' + $run.Substring(0, 8) + '.sock'

function Write-Record([string] $Stage, [string] $Status, $ErrorObject = $null, $Extra = @{}) {
    $record = [ordered]@{
        run = $run; case = $script:caseIndex; stage = $Stage; status = $Status
        bind_path = $script:bindPath; connect_path = $script:connectPath
    }
    if ($ErrorObject) {
        $exception = if ($ErrorObject -is [Management.Automation.ErrorRecord]) { $ErrorObject.Exception } else { $ErrorObject }
        while ($exception.InnerException) { $exception = $exception.InnerException }
        $record['exception'] = $exception.GetType().FullName
        $record['message'] = $exception.Message
        $record['hresult'] = $exception.HResult
        if ($exception -is [Net.Sockets.SocketException]) {
            $record['native_error'] = $exception.NativeErrorCode
            $record['socket_error'] = $exception.SocketErrorCode.ToString()
        }
    }
    foreach ($key in $Extra.Keys) { $record[$key] = $Extra[$key] }
    $record | ConvertTo-Json -Compress -Depth 5
}

for ($caseIndex = 0; $caseIndex -lt $BindDirectory.Count; $caseIndex++) {
    $bindPath = [IO.Path]::GetFullPath([IO.Path]::Combine($BindDirectory[$caseIndex], $basename))
    $connectPath = [IO.Path]::GetFullPath([IO.Path]::Combine($ConnectDirectory[$caseIndex], $basename))
    $listener = $null; $client = $null; $accepted = $null; $acceptTask = $null
    $createdEndpoint = $false
    $stage = 'directory'
    try {
        $bindItem = Get-Item -LiteralPath $BindDirectory[$caseIndex] -Force
        $connectItem = Get-Item -LiteralPath $ConnectDirectory[$caseIndex] -Force
        if (-not $bindItem.PSIsContainer -or -not $connectItem.PSIsContainer) { throw 'Both paths must be directories.' }
        Write-Record 'environment' 'info' -Extra @{
            os = [Environment]::OSVersion.VersionString; dotnet = [Environment]::Version.ToString()
            powershell = $PSVersionTable.PSVersion.ToString(); user = $identity.Name; sid = $identity.User.Value
            elevated = $principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)
            pid = $PID; bind_chars = $bindPath.Length; bind_utf8_bytes = [Text.Encoding]::UTF8.GetByteCount($bindPath)
            connect_chars = $connectPath.Length; connect_utf8_bytes = [Text.Encoding]::UTF8.GetByteCount($connectPath)
            bind_attributes = $bindItem.Attributes.ToString(); bind_link_target = $bindItem.LinkTarget
            connect_attributes = $connectItem.Attributes.ToString(); connect_link_target = $connectItem.LinkTarget
        }
        $stage = 'socket'
        $listener = [Net.Sockets.Socket]::new([Net.Sockets.AddressFamily]::Unix, [Net.Sockets.SocketType]::Stream, [Net.Sockets.ProtocolType]::Unspecified)
        $client = [Net.Sockets.Socket]::new([Net.Sockets.AddressFamily]::Unix, [Net.Sockets.SocketType]::Stream, [Net.Sockets.ProtocolType]::Unspecified)
        Write-Record $stage 'ok'
        $stage = 'bind'
        $listener.Bind([Net.Sockets.UnixDomainSocketEndPoint]::new($bindPath))
        $createdEndpoint = $true
        Write-Record $stage 'ok'
        $stage = 'listen'
        $listener.Listen(1)
        Write-Record $stage 'ok'
        $acceptTask = $listener.AcceptAsync()
        $stage = 'connect'
        $connectTask = $client.ConnectAsync([Net.Sockets.UnixDomainSocketEndPoint]::new($connectPath))
        if (-not $connectTask.Wait($TimeoutMs)) { throw [TimeoutException]::new('connect deadline exceeded') }
        $connectTask.GetAwaiter().GetResult()
        Write-Record $stage 'ok'
        $stage = 'accept'
        if (-not $acceptTask.Wait($TimeoutMs)) { throw [TimeoutException]::new('accept deadline exceeded') }
        $accepted = $acceptTask.GetAwaiter().GetResult()
        Write-Record $stage 'ok'
        $stage = 'transfer'
        $client.SendTimeout = $TimeoutMs
        $accepted.ReceiveTimeout = $TimeoutMs
        $sent = $client.Send([byte[]]@(0x4f))
        $buffer = [byte[]]::new(1)
        $received = $accepted.Receive($buffer)
        if ($sent -ne 1 -or $received -ne 1 -or $buffer[0] -ne 0x4f) { throw 'payload mismatch' }
        Write-Record $stage 'ok'
    } catch {
        Write-Record $stage 'error' $_
    } finally {
        if ($accepted) { $accepted.Dispose() }
        if ($client) { $client.Dispose() }
        if ($listener) { $listener.Dispose() }
        if ($acceptTask) {
            try {
                if ($acceptTask.Wait($TimeoutMs)) { $acceptTask.GetAwaiter().GetResult().Dispose() }
            } catch { } # Disposal cancels pending Accept; the failing stage is recorded above.
        }
        if ($createdEndpoint) {
            try {
                if (Test-Path -LiteralPath $bindPath) { Remove-Item -LiteralPath $bindPath -Force }
                Write-Record 'cleanup' 'ok'
            } catch { Write-Record 'cleanup' 'error' $_ }
        }
    }
}
