# Spec RPM Karta. Dipakai workflow release:
#   rpmbuild -bb --define "_topdir <dir>" --define "_version 0.2.0" packaging/karta.spec
# Binary karta, karta.desktop, dan ikon 512px dijadikan sumber melalui
# --define "_bindir_src", "_desktop_src", "_icon_src" (path absolut).
Name:           karta
Version:        %{_version}
Release:        1%{?dist}
Summary:        Email client desktop native yang cepat dan ringan

License:        Proprietary
URL:            https://github.com/dchya24/karta
Source0:        %{_bindir_src}
Source1:        %{_desktop_src}
Source2:        %{_icon_src}

%description
Karta adalah email client desktop pure native (tanpa Electron/WebView)
yang dirancang untuk kecepatan ekstrem, konsumsi memori minimal, dan
navigasi keyboard-first. Pencarian lokal instan berbasis SQLite FTS5.

%prep

%build

%install
mkdir -p %{buildroot}%{_bindir}
mkdir -p %{buildroot}%{_datadir}/applications
mkdir -p %{buildroot}%{_datadir}/icons/hicolor/512x512/apps
install -m 0755 %{SOURCE0} %{buildroot}%{_bindir}/karta
install -m 0644 %{SOURCE1} %{buildroot}%{_datadir}/applications/karta.desktop
install -m 0644 %{SOURCE2} %{buildroot}%{_datadir}/icons/hicolor/512x512/apps/karta.png

%files
%{_bindir}/karta
%{_datadir}/applications/karta.desktop
%{_datadir}/icons/hicolor/512x512/apps/karta.png
