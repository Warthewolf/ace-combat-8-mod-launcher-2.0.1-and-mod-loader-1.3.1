/* Test-only stand-in. Never deploy this DLL into the game. */
void* ctor_stub(void* self) { return self; }
void dtor_stub(void* self) { (void)self; }
