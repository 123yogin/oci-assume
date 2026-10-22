# Recorded at release time: vhs docs/demo.tape  (writes docs/demo.gif)
Output docs/demo.gif
Set Shell "zsh"
Set FontSize 16
Set Width 1000
Set Height 500
Set TypingSpeed 60ms

Type `eval "$(oci-assume init zsh)"`
Enter
Type "oci-assume dev"
Enter
Sleep 3s
Type "oci os ns get"
Enter
Sleep 3s
Type "oci-assume status"
Enter
Sleep 3s
