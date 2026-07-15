OpsKat - Portable Mode
======================

This folder is what makes OpsKat portable.

While this `data` folder sits next to opskat.exe, OpsKat keeps all of its
state here instead of in %LOCALAPPDATA%\opskat:

  opskat.db     your assets, groups, credentials, snippets and audit log
  master.key    the encryption key for the credentials inside opskat.db
  config.json   application settings
  logs/         application logs

You can move or copy the whole OpsKat folder - to a USB drive, another disk,
or another machine - and it will keep working with the same data. OpsKat in
portable mode does not write to the Windows registry, does not modify your
PATH, and does not store anything in the Windows Credential Manager, so the
machine you run it on is left untouched.

Deleting this folder
--------------------

If you delete this folder, OpsKat stops being portable. On the next start it
falls back to the normal per-user location, %LOCALAPPDATA%\opskat, and starts
from an empty database. Your existing data is NOT migrated there - it is
simply no longer used. Keep this folder together with opskat.exe.

To turn portable mode back on later, create an empty folder named `data` next
to opskat.exe again.

Security
--------

master.key is stored here in plain text, in the same folder as the database it
decrypts. That is precisely what makes this folder portable, and it is a
deliberate trade-off: anyone who can read this folder can read every
credential you have stored in OpsKat.

The installer build behaves differently - it keeps master.key in the Windows
Credential Manager, separate from the database, so copying the database alone
is not enough to decrypt it.

Therefore: do not put this folder on shared or synced storage, and prefer an
encrypted volume (for example BitLocker To Go) if you carry it on a USB drive.

opsctl
------

opsctl.exe next to opskat.exe is the OpsKat command line interface. It is
portable too: run it from this folder and it reads and writes this same data
folder, so the app and the CLI always agree on one database.
