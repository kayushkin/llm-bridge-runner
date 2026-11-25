# compact-wsl.ps1: shut WSL down, compact every WSL distribution's disk
# file, start WSL again.
#
# Run only by the scheduled task \llm-bridge\WSL compact, elevated, which
# llm-bridge-disk-usage installs with this file in a folder only
# administrators can change. It takes no arguments and reads nothing a
# program without administrator rights could have written there, except
# where WSL keeps each distribution (the user's own registry), and it
# touches only .vhdx files found that way.
#
# It records where it is in status.json beside itself, so the runner, which
# this stops with the rest of WSL, can report how it went once WSL is back.
# Whatever happens, it starts WSL again through the task \llm-bridge\WSL start,
# which runs as the owner without administrator rights, as WSL normally runs.

$ErrorActionPreference = 'Stop'
$here = Split-Path -Parent $MyInvocation.MyCommand.Path
$statusFile = Join-Path $here 'status.json'
$logFile = Join-Path $here 'last-run.log'
$diskpartScript = Join-Path $here 'diskpart-commands.txt'
$utf8 = New-Object System.Text.UTF8Encoding $false

function Now { (Get-Date).ToUniversalTime().ToString('o') }
function Save-Status {
    $json = $status | ConvertTo-Json -Depth 6
    [System.IO.File]::WriteAllText("$statusFile.tmp", $json, $utf8)
    Move-Item -LiteralPath "$statusFile.tmp" -Destination $statusFile -Force
}
function Write-Log([string]$text) {
    [System.IO.File]::AppendAllText($logFile, ("{0} {1}`r`n" -f (Now), $text), $utf8)
}
function Invoke-Diskpart([string[]]$commands) {
    [System.IO.File]::WriteAllLines($diskpartScript, $commands, [System.Text.Encoding]::ASCII)
    $output = & diskpart.exe /s $diskpartScript 2>&1 | Out-String
    $code = $LASTEXITCODE
    Write-Log ("diskpart ({0}) exited {1}:`r`n{2}" -f ($commands -join '; '), $code, $output)
    return $code
}

[System.IO.File]::WriteAllText($logFile, '', $utf8)
$status = [ordered]@{
    state         = 'running'
    step          = 'waiting for the runner to answer the bridge'
    started_at    = Now
    finished_at   = $null
    disks         = @()
    error         = $null
    restarted_wsl = $false
}
Save-Status

$current = $null
try {
    # The runner answers the bridge before WSL stops under it.
    Start-Sleep -Seconds 10

    $disks = @()
    foreach ($key in Get-ChildItem 'HKCU:\Software\Microsoft\Windows\CurrentVersion\Lxss') {
        $distribution = Get-ItemProperty -LiteralPath $key.PSPath
        $fileName = 'ext4.vhdx'
        if ($distribution.VhdFileName) { $fileName = $distribution.VhdFileName }
        $basePath = [string]$distribution.BasePath
        if ($basePath.StartsWith('\\?\')) { $basePath = $basePath.Substring(4) }
        $file = Join-Path $basePath $fileName
        if ($file -notmatch '\.vhdx$') { throw "the disk of $($distribution.DistributionName) is not a .vhdx file: $file" }
        if (-not (Test-Path -LiteralPath $file -PathType Leaf)) { throw "the disk of $($distribution.DistributionName) is not at $file" }
        $disks += [ordered]@{
            distribution   = [string]$distribution.DistributionName
            disk_file_path = $file
            bytes_before   = (Get-Item -LiteralPath $file).Length
            bytes_after    = 0
            compacted      = $false
        }
    }
    if ($disks.Count -eq 0) { throw 'found no WSL distribution to compact' }
    $status.disks = $disks
    $status.step = 'shutting down WSL'
    Save-Status
    Write-Log 'wsl --shutdown'

    & wsl.exe --shutdown
    if ($LASTEXITCODE -ne 0) { throw "wsl --shutdown exited $LASTEXITCODE" }
    # The disk files stay open until WSL's virtual machine has gone.
    $deadline = (Get-Date).AddMinutes(2)
    while (Get-Process -Name 'vmmem', 'vmmemWSL' -ErrorAction SilentlyContinue) {
        if ((Get-Date) -gt $deadline) { throw "WSL's virtual machine was still running two minutes after wsl --shutdown" }
        Start-Sleep -Seconds 2
    }

    foreach ($disk in $disks) {
        $current = $disk
        $status.step = "compacting $($disk.distribution)"
        Save-Status
        $code = Invoke-Diskpart @(
            "select vdisk file=`"$($disk.disk_file_path)`"",
            'attach vdisk readonly',
            'compact vdisk',
            'detach vdisk'
        )
        if ($code -ne 0) { throw "diskpart could not compact $($disk.disk_file_path) (exit $code); last-run.log beside this script has its output" }
        $disk.bytes_after = (Get-Item -LiteralPath $disk.disk_file_path).Length
        $disk.compacted = $true
        $current = $null
        Save-Status
    }
    $status.state = 'finished'
}
catch {
    $status.state = 'failed'
    $status.error = $_.Exception.Message
    Write-Log ("failed: {0}" -f $_.Exception.Message)
    if ($current) {
        # Never leave a disk attached to Windows: WSL could not start on it.
        Invoke-Diskpart @("select vdisk file=`"$($current.disk_file_path)`"", 'detach vdisk') | Out-Null
    }
}
finally {
    $status.step = 'starting WSL again'
    Save-Status
    try {
        Start-ScheduledTask -TaskPath '\llm-bridge\' -TaskName 'WSL start'
        $status.restarted_wsl = $true
        Write-Log 'started the task \llm-bridge\WSL start'
    }
    catch {
        $message = "could not start WSL again: $($_.Exception.Message)"
        if ($status.error) { $status.error = "$($status.error); $message" } else { $status.error = $message }
        Write-Log $message
    }
    $status.step = ''
    $status.finished_at = Now
    Save-Status
}
