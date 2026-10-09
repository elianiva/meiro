Name:           meiro
Version:        %{app_version}
Release:        1%{?dist}
Summary:        Native desktop client for YouTube Music
License:        GPL-3.0-only
URL:            https://github.com/elianiva/meiro
Source0:         %{archive}
BuildArch:       %{rpm_arch}
Requires:        webkit2gtk4.1

%description
Meiro is a small, fast native desktop client for YouTube Music.

%prep

%build

%install
rm -rf %{buildroot}
install -d %{buildroot}/opt/meiro
%{__tar} -xzf %{SOURCE0} -C %{buildroot}/opt/meiro
install -d %{buildroot}%{_bindir}
cat >%{buildroot}%{_bindir}/meiro <<'EOF'
#!/bin/sh
exec /opt/meiro/meiro "$@"
EOF
chmod 0755 %{buildroot}%{_bindir}/meiro
install -Dpm 0644 %{buildroot}/opt/meiro/meiro.desktop %{buildroot}%{_datadir}/applications/meiro.desktop
install -Dpm 0644 %{buildroot}/opt/meiro/meiro.png %{buildroot}%{_datadir}/icons/hicolor/256x256/apps/meiro.png

%files
/opt/meiro
%{_bindir}/meiro
%{_datadir}/applications/meiro.desktop
%{_datadir}/icons/hicolor/256x256/apps/meiro.png

%changelog
* Wed Jan 01 2025 Meiro <hello@meiro.app> - %{version}-1
- Package Meiro
